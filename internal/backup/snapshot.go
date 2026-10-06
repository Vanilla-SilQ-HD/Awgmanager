package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// SnapshotDir — снимки перед обновлением, ВНУТРИ каталога данных: уходят
	// вместе с ним при opkg remove и rm -rf, в отличие от прежних копий
	// <dataDir>.pre-restore-* рядом (#953). Модулей ядра и бинаря sing-box в
	// снимке нет — их не несёт и обычный бэкап (hardwareBound).
	SnapshotDir = "update-snapshots"
	// SnapshotKeep — сколько последних снимков хранится; старшие удаляются
	// после записи нового.
	SnapshotKeep = 3
	// snapshotSpareBytes — запас свободного места, который снимок обязан
	// оставить: следом opkg распаковывает пакет на тот же раздел.
	snapshotSpareBytes = 32 << 20
	snapshotPrefix     = "before-update-"
	snapshotStamp      = "20060102-150405"
)

// ErrSnapshotNotFound — снимка с таким id нет (или id не похож на снимок).
var ErrSnapshotNotFound = errors.New("снимок не найден")

var snapshotName = regexp.MustCompile(`^before-update-\d{8}-\d{6}\.tar\.gz$`)

// Snapshot описывает снимок каталога данных, снятый перед обновлением.
type Snapshot struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"createdAt"`
	AppVersion string    `json:"appVersion,omitempty"`
	Size       int64     `json:"size"`
}

var (
	snapshotMu sync.Mutex
	// availableBytesFunc подменяется в тестах.
	availableBytesFunc = availableBytes
)

// TakeUpdateSnapshot сохраняет архив каталога данных (тот же, что отдаёт
// экспорт) в <dataDir>/update-snapshots и оставляет SnapshotKeep последних.
// Места мало — снимок не пишется вовсе, а не забивает раздел до отказа
// следующей за ним установки пакета. Недописанный файл удаляется.
func TakeUpdateSnapshot(dataDir, appVersion string, now time.Time) (Snapshot, error) {
	if err := CheckDataDir(dataDir); err != nil {
		return Snapshot{}, err
	}
	dataDir = filepath.Clean(strings.TrimSpace(dataDir))
	snapshotMu.Lock()
	defer snapshotMu.Unlock()

	dir := filepath.Join(dataDir, SnapshotDir)
	// 0700/0600: в архиве приватные ключи туннелей открытым текстом.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Snapshot{}, err
	}
	if leftovers, err := filepath.Glob(filepath.Join(dir, "*.tmp")); err == nil {
		for _, p := range leftovers {
			_ = os.Remove(p)
		}
	}

	need, err := exportSize(dataDir)
	if err != nil {
		return Snapshot{}, err
	}
	if avail, ok := availableBytesFunc(dir); ok && avail < need+snapshotSpareBytes {
		return Snapshot{}, fmt.Errorf("мало места: снимку нужно до %d МБ с запасом, свободно %d МБ",
			(need+snapshotSpareBytes)>>20, avail>>20)
	}

	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	id := snapshotPrefix + now.Format(snapshotStamp) + ".tar.gz"
	final := filepath.Join(dir, id)
	tmp := final + ".tmp"
	if err := writeSnapshot(dataDir, appVersion, tmp); err != nil {
		_ = os.Remove(tmp)
		return Snapshot{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return Snapshot{}, err
	}
	pruneSnapshots(dir, SnapshotKeep)

	snap := Snapshot{ID: id, CreatedAt: now.Truncate(time.Second), AppVersion: strings.TrimSpace(appVersion)}
	if info, err := os.Stat(final); err == nil {
		snap.Size = info.Size()
	}
	return snap, nil
}

func writeSnapshot(dataDir, appVersion, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := Export(dataDir, appVersion, f); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// exportSize — сколько несжатых байт положил бы в архив Export: оценка
// сверху, сжатый снимок меньше.
func exportSize(dataDir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dataDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dataDir, p)
		if err != nil || rel == "." {
			return err
		}
		if shouldSkip(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Файл, исчезнувший между чтением каталога и stat, оценку не валит.
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

// snapshotIDs возвращает имена снимков в dir, новые первыми (метка времени
// в имени сортируется как строка).
func snapshotIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.Type().IsRegular() && snapshotName.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids, nil
}

func pruneSnapshots(dir string, keep int) {
	ids, err := snapshotIDs(dir)
	if err != nil || len(ids) <= keep {
		return
	}
	for _, id := range ids[keep:] {
		_ = os.Remove(filepath.Join(dir, id))
	}
}

// ListSnapshots возвращает снимки, новые первыми. Нечитаемый манифест не
// прячет снимок: время берётся из имени, версия остаётся пустой.
func ListSnapshots(dataDir string) ([]Snapshot, error) {
	dir := filepath.Join(filepath.Clean(strings.TrimSpace(dataDir)), SnapshotDir)
	ids, err := snapshotIDs(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Snapshot, 0, len(ids))
	for _, id := range ids {
		p := filepath.Join(dir, id)
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		snap := Snapshot{ID: id, Size: info.Size()}
		stamp := strings.TrimSuffix(strings.TrimPrefix(id, snapshotPrefix), ".tar.gz")
		if t, err := time.Parse(snapshotStamp, stamp); err == nil {
			snap.CreatedAt = t
		}
		if f, err := os.Open(p); err == nil {
			if m, err := readManifest(f); err == nil {
				snap.AppVersion = m.AppVersion
			}
			f.Close()
		}
		out = append(out, snap)
	}
	return out, nil
}

// snapshotPath проверяет id и возвращает путь к снимку. Id — только имя
// по шаблону, поэтому выйти им за пределы каталога снимков нельзя.
func snapshotPath(dataDir, id string) (string, error) {
	if !snapshotName.MatchString(id) {
		return "", ErrSnapshotNotFound
	}
	p := filepath.Join(filepath.Clean(strings.TrimSpace(dataDir)), SnapshotDir, id)
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrSnapshotNotFound
	}
	return p, nil
}

// OpenSnapshot открывает снимок на чтение; закрывает вызывающий.
func OpenSnapshot(dataDir, id string) (*os.File, error) {
	p, err := snapshotPath(dataDir, id)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// DeleteSnapshot удаляет снимок.
func DeleteSnapshot(dataDir, id string) error {
	snapshotMu.Lock()
	defer snapshotMu.Unlock()
	p, err := snapshotPath(dataDir, id)
	if err != nil {
		return err
	}
	return os.Remove(p)
}
