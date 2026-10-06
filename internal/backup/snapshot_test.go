package backup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func snapshotDataDir(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "settings.json"), []byte(`{"v":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func withAvailable(t *testing.T, avail int64, ok bool) {
	t.Helper()
	orig := availableBytesFunc
	availableBytesFunc = func(string) (int64, bool) { return avail, ok }
	t.Cleanup(func() { availableBytesFunc = orig })
}

func TestTakeUpdateSnapshotWritesRestorableArchive(t *testing.T) {
	dataDir := snapshotDataDir(t)
	now := time.Date(2026, 10, 6, 12, 30, 45, 0, time.UTC)
	snap, err := TakeUpdateSnapshot(dataDir, "2.19.9", now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ID != "before-update-20261006-123045.tar.gz" || snap.AppVersion != "2.19.9" || snap.Size == 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
	p := filepath.Join(dataDir, SnapshotDir, snap.ID)
	if info, err := os.Stat(p); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("файл снимка: %v %v", info, err)
	}

	// Снимок — обычный бэкап: Restore принимает его, а сам каталог снимков
	// переживает восстановление.
	if err := os.WriteFile(filepath.Join(dataDir, "settings.json"), []byte(`{"v":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := OpenSnapshot(dataDir, snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := Restore(dataDir, f); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, filepath.Join(dataDir, "settings.json"))); got != `{"v":1}` {
		t.Errorf("settings.json после отката = %s", got)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("снимок пропал после восстановления: %v", err)
	}
}

func TestExportSkipsSnapshots(t *testing.T) {
	dataDir := snapshotDataDir(t)
	if _, err := TakeUpdateSnapshot(dataDir, "2.19.9", time.Time{}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Export(dataDir, "2.19.9", &buf); err != nil {
		t.Fatal(err)
	}
	names := strings.Join(tarNames(t, buf.Bytes()), " ")
	if strings.Contains(names, SnapshotDir) {
		t.Errorf("снимки попали в архив: %s", names)
	}
	if !strings.Contains(names, "settings.json") {
		t.Errorf("settings.json нет в архиве: %s", names)
	}
}

func TestTakeUpdateSnapshotKeepsLatest(t *testing.T) {
	dataDir := snapshotDataDir(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < SnapshotKeep+2; i++ {
		if _, err := TakeUpdateSnapshot(dataDir, "2.19.9", base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := ListSnapshots(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != SnapshotKeep {
		t.Fatalf("снимков %d, ожидалось %d", len(list), SnapshotKeep)
	}
	want := base.Add(time.Duration(SnapshotKeep+1) * time.Hour)
	if !list[0].CreatedAt.Equal(want) || list[0].AppVersion != "2.19.9" {
		t.Errorf("первым должен идти самый новый: %+v", list[0])
	}
}

func TestTakeUpdateSnapshotRefusesWhenLowOnSpace(t *testing.T) {
	dataDir := snapshotDataDir(t)
	withAvailable(t, snapshotSpareBytes, true)
	if _, err := TakeUpdateSnapshot(dataDir, "2.19.9", time.Time{}); err == nil || !strings.Contains(err.Error(), "мало места") {
		t.Fatalf("ожидался отказ по месту, err = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dataDir, SnapshotDir))
	if len(entries) != 0 {
		t.Errorf("после отказа в каталоге снимков что-то осталось: %v", entries)
	}
}

func TestTakeUpdateSnapshotRemovesPartialFile(t *testing.T) {
	dataDir := snapshotDataDir(t)
	dir := filepath.Join(dataDir, SnapshotDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "before-update-20250101-000000.tar.gz.tmp")
	if err := os.WriteFile(stale, []byte("oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := TakeUpdateSnapshot(dataDir, "2.19.9", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("недописанный снимок прошлой попытки не убран: %v", err)
	}
}

func TestSnapshotIDCannotEscapeDir(t *testing.T) {
	dataDir := snapshotDataDir(t)
	for _, id := range []string{"../settings.json", "settings.json", "before-update-20260101-000000.tar.gz/../../x", ""} {
		if _, err := OpenSnapshot(dataDir, id); !errors.Is(err, ErrSnapshotNotFound) {
			t.Errorf("OpenSnapshot(%q) = %v", id, err)
		}
		if err := DeleteSnapshot(dataDir, id); !errors.Is(err, ErrSnapshotNotFound) {
			t.Errorf("DeleteSnapshot(%q) = %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "settings.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteSnapshot(t *testing.T) {
	dataDir := snapshotDataDir(t)
	snap, err := TakeUpdateSnapshot(dataDir, "2.19.9", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteSnapshot(dataDir, snap.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := ListSnapshots(dataDir); len(list) != 0 {
		t.Errorf("снимок не удалён: %+v", list)
	}
	if err := DeleteSnapshot(dataDir, snap.ID); !errors.Is(err, ErrSnapshotNotFound) {
		t.Errorf("повторное удаление = %v", err)
	}
}

func TestListSnapshotsWithoutDir(t *testing.T) {
	list, err := ListSnapshots(snapshotDataDir(t))
	if err != nil || len(list) != 0 {
		t.Fatalf("list = %v, err = %v", list, err)
	}
}
