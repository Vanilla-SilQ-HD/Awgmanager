package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const FilePermission = 0644

// SecretFilePermission — режим файлов, несущих секреты ОТКРЫТЫМ текстом:
// settings.json и его спутники (.bak, .corrupt) хранят apiKey панели и
// приватные ключи пиров и managed-серверов. 0644 делал их читаемыми любым
// пользователем роутера, а демон работает от root — послаблять права было
// нечем.
const SecretFilePermission = 0600
const DirPermission = 0755

// AtomicWrite writes data to path atomically using temp file + rename.
func AtomicWrite(path string, data []byte) error {
	return AtomicWritePerm(path, data, FilePermission)
}

// AtomicWritePerm is like AtomicWrite but with custom file permissions.
//
// The temp file and the containing directory are fsync'ed before/after the
// rename: on routers state lives on flash with ext4 delayed allocation, and
// without the syncs a power loss inside the writeback window can leave a
// zero-length file under the final name.
func AtomicWritePerm(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, DirPermission); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	// Имя временного файла ДОЛЖНО быть уникальным по построению. Прежнее
	// `<путь>.tmp.<pid>.<UnixNano>` уникальным не было: два писателя в одном
	// процессе успевают получить одну наносекунду (на ревью воспроизвелось —
	// 3 отказа записи на 200 итераций с двумя писателями), и тогда второй
	// os.Rename падает с ENOENT, потому что первый уже унёс общий временный
	// файл. Цена промаха разная: для настроек это сорванная правка, для файла
	// секрета устройства — потерянный ключ подписки на ПЕРВОЙ записи.
	//
	// os.CreateTemp даёт уникальность самим созданием файла (O_EXCL с
	// перебором), поэтому гонки не остаётся вовсе. Каталог тот же, что у
	// цели, — иначе rename ушёл бы между файловыми системами и перестал быть
	// атомарным.
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()
	// Закрываем сразу: writeFileSync открывает файл сам, а держать лишний
	// дескриптор до конца функции незачем.
	_ = f.Close()

	// CreateTemp всегда создаёт файл с 0600 — режим цели ставим явно, иначе
	// общие файлы (не секретные) остались бы строже, чем задумано, а
	// секретные не стали бы строже сами.
	if err := os.Chmod(tmpPath, perm); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}

	if err := writeFileSync(tmpPath, data, perm); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp to target: %w", err)
	}

	syncDir(dir)
	return nil
}

func writeFileSync(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// CommitFile делает готовый временный файл tmp файлом path: fsync
// содержимого, rename, fsync каталога — порядок AtomicWritePerm для файлов,
// которые пишутся потоком (загрузки бинарей, гео-списков, наборов правил).
// Без синков пропадание питания в окне writeback оставляет под окончательным
// именем пустой файл (F375). tmp и path — в одной файловой системе.
func CommitFile(tmp, path string) error {
	f, err := os.Open(tmp)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir fsyncs a directory so a preceding rename survives power loss.
// Best-effort: some filesystems reject directory fsync.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	d.Sync()
	d.Close()
}

// QuarantineCorrupt уносит повреждённый файл состояния в <path>.corrupt[.N] и
// сообщает об этом в журнал приложения. Зовут его хранилища, которые иначе
// молча сбросились бы к умолчаниям, а на следующем сохранении затёрли бы
// повреждённый файл окончательно.
//
// После переименования к файлу больше никто не обращается: читатели ищут
// .json. Копия остаётся на диске не ради того, чтобы пользователь чинил её
// руками — до файла на роутере он не пойдёт, — а чтобы данные можно было
// достать, если он попросит помощи.
func QuarantineCorrupt(path string, parseErr error) {
	quarantine, err := quarantineFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage: %s is corrupt (%v); quarantine failed: %v\n", path, parseErr, err)
		// Сохранить в карантин не вышло — файл остаётся на месте и будет прочитан
		// снова, то есть сообщение повторится на каждом чтении. Говорим об
		// этом прямо: сам он не починится.
		recordNotice("quarantine", filepath.Base(path), fmt.Sprintf(
			"Файл %s повреждён (%v) и не поддаётся автоматическому исправлению: %v. Настройки из него недоступны.",
			filepath.Base(path), parseErr, err))
		return
	}
	fmt.Fprintf(os.Stderr, "storage: %s is corrupt (%v); moved to %s, continuing with defaults\n", path, parseErr, quarantine)
	// Текст адресован человеку в журнале приложения, а не инженеру в консоли:
	// звать «восстановить вручную» бессмысленно — до файла на роутере никто не
	// пойдёт. Говорим прямо, что запись потеряна и что делать дальше; сам файл
	// уже переименован, то есть читать его больше никто не будет.
	recordNotice("quarantine", filepath.Base(path), fmt.Sprintf(
		"Файл %s повреждён и больше не используется (%v). Настройки из него потеряны — создайте их заново. Повреждённая копия сохранена рядом как %s.",
		filepath.Base(path), parseErr, filepath.Base(quarantine)))
}

// Serialize link/unlink pairs, including callers with different store locks.
// As with other storage operations, callers must serialize source writes with
// loading/quarantine; this lock does not coordinate external file editors.
var quarantineMu sync.Mutex

// quarantineFile uses link's atomic no-replace semantics, supported by Entware's
// EXT/UBIFS filesystems (and already used for settings.json.bak). It needs no
// renameat2 support and copies no file data.
// Unsupported filesystems fail without removing the source; never fall back to
// an overwriting rename. A crash before unlink leaves two complete names for
// the same inode. Existing copies are retained without automatic rotation.
func quarantineFile(path string) (string, error) {
	quarantineMu.Lock()
	defer quarantineMu.Unlock()

	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("quarantine %s: not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	// Tighten legacy files BEFORE publishing another name for their secrets.
	// Preserve any permissions stricter than 0600 as well.
	err = f.Chmod(info.Mode().Perm() & SecretFilePermission)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}

	quarantine := path + ".corrupt"
	for n := 1; ; n++ {
		if err := os.Link(path, quarantine); err != nil {
			if !os.IsExist(err) {
				return "", err
			}
			// A directory here is unexpected filesystem state, not a saved
			// copy. Keep the existing failure/recovery behaviour for it.
			fi, statErr := os.Lstat(quarantine)
			if statErr != nil || fi.IsDir() {
				return "", err
			}
			quarantine = fmt.Sprintf("%s.corrupt.%d", path, n)
			continue
		}
		break
	}
	// Persist the new directory entry before removing the original. On any
	// failure leave the complete link in place as well as the source.
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("linked to %s, source retained: %w", quarantine, err)
	}
	err = d.Sync()
	closeErr = d.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("linked to %s, source retained: %w", quarantine, err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("linked to %s, remove source: %w", quarantine, err)
	}
	syncDir(filepath.Dir(path))
	return quarantine, nil
}
