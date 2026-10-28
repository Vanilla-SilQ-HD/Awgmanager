package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_RepeatedCorruptionPreservesCopies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	backup := []byte(fmt.Sprintf(`{"schemaVersion":%d,"usageLevel":"expert"}`, CurrentSchemaVersion))
	if err := os.WriteFile(path+".bak", backup, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("{broken-%d", i)), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := NewSettingsStore(dir).Load()
		if err != nil || got.UsageLevel != "expert" {
			t.Fatalf("backup restoration: %v, %v", got, err)
		}
		for j := 0; j <= i; j++ {
			name := path + ".corrupt"
			if j > 0 {
				name += fmt.Sprintf(".%d", j)
			}
			assertQuarantine(t, name, fmt.Sprintf("{broken-%d", j))
		}
	}
}

// Битый settings.json карантинится в .corrupt, значения восстанавливаются из .bak
// (hardlink, сделанный прошлой записью), уведомление backup-restore записано.
func TestLoad_RestoresFromBakWhenCorrupt(t *testing.T) {
	dir := t.TempDir()
	store := NewSettingsStore(dir)
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(s *Settings) error { s.UsageLevel = "expert"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(s *Settings) error { s.UsageLevel = "expert"; s.ApiKey = "k-1"; return nil }); err != nil {
		t.Fatal(err) // вторая запись — .bak содержит первую версию с expert
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Буфер pendingNotices к этому моменту НЕ пуст (quarantine-уведомления соседних тестов
	// пакета, notices.go:50-59 выгружает его при подключении sink) — сперва осушить.
	SetNoticeSink(func(Notice) {})
	var notices []Notice
	SetNoticeSink(func(n Notice) { notices = append(notices, n) })
	t.Cleanup(func() { SetNoticeSink(nil) })

	fresh := NewSettingsStore(dir)
	got, err := fresh.Load()
	if err != nil {
		t.Fatalf("Load обязан восстановиться из .bak: %v", err)
	}
	if got.UsageLevel != "expert" {
		t.Fatalf("восстановлено %+v, want usageLevel=expert из .bak", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json.corrupt")); err != nil {
		t.Fatal("битый файл обязан уйти в .corrupt")
	}
	if len(notices) == 0 || notices[0].Action != "backup-restore" ||
		!strings.Contains(notices[0].Message, "quarantined to "+filepath.Join(dir, "settings.json.corrupt")) {
		t.Fatalf("notices = %+v", notices)
	}
	// Восстановленное сразу переписано на диск валидным JSON: свежий стор
	// поднимает именно восстановленное значение, а не дефолты (без
	// пересохранения settings.json остался бы в карантине, и Load отдал бы
	// дефолтную запись, на которой прежняя проверка была зелёной всегда).
	again, err := NewSettingsStore(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if again.UsageLevel != "expert" {
		t.Fatalf("после восстановления на диске %+v, want usageLevel=expert", again)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json обязан быть переписан: %v", err)
	}
	var onDisk Settings
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("settings.json не валидный JSON: %v (%s)", err, raw)
	}
	if onDisk.UsageLevel != "expert" {
		t.Fatalf("в файле %+v, want usageLevel=expert", onDisk)
	}
}

// Отказ карантина (место .corrupt занято непустым каталогом) больше не выдаётся
// за «quarantined to»: битый файл
// остаётся на месте, восстановление из .bak всё равно идёт, уведомление говорит правду.
func TestLoad_CorruptQuarantineFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	store := NewSettingsStore(dir)
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(s *Settings) error { s.UsageLevel = "expert"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(s *Settings) error { s.ApiKey = "k-1"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(dir, "settings.json.bak")
	backupBefore, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "settings.json.corrupt", "busy"), 0o755); err != nil {
		t.Fatal(err)
	}
	SetNoticeSink(func(Notice) {})
	var notices []Notice
	SetNoticeSink(func(n Notice) { notices = append(notices, n) })
	t.Cleanup(func() { SetNoticeSink(nil) })

	got, err := NewSettingsStore(dir).Load()
	if err != nil {
		t.Fatalf("Load обязан восстановиться из .bak и при отказе карантина: %v", err)
	}
	if got.UsageLevel != "expert" {
		t.Fatalf("восстановлено %+v", got)
	}
	backupAfter, err := os.ReadFile(backupPath)
	if err != nil || string(backupAfter) != string(backupBefore) {
		t.Fatalf("good backup replaced after failed quarantine: %q, %v", backupAfter, err)
	}
	main, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil || !json.Valid(main) {
		t.Fatalf("invalid restored main: %q, %v", main, err)
	}
	for i := 0; i < 2; i++ {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{broken again"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSettingsStore(dir).Load(); err != nil {
			t.Fatal(err)
		}
	}
	if len(notices) == 0 || !strings.Contains(notices[0].Message, "corrupt file left in place") ||
		strings.Contains(notices[0].Message, "quarantined to") {
		t.Fatalf("уведомление врёт о карантине: %+v", notices)
	}
}

func TestLoad_RecoveryWriteFailurePreservesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	backup := fmt.Sprintf(`{"schemaVersion":%d,"usageLevel":"expert"}`, CurrentSchemaVersion)
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".bak", []byte(backup), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".corrupt", 0700); err != nil {
		t.Fatal(err)
	}
	SetNoticeSink(func(Notice) {})
	SetNoticeSink(func(n Notice) {
		if n.Action != "backup-restore" {
			return
		}
		// Force rename failure on every OS and under root, after reading .bak.
		if err := os.Rename(path, path+".held"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(func() { SetNoticeSink(nil) })
	if _, err := NewSettingsStore(dir).Load(); err == nil {
		t.Fatal("expected write failure")
	}
	assertQuarantine(t, path+".bak", backup)
	assertQuarantine(t, path+".held", "{broken")
	SetNoticeSink(func(Notice) {})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".held", path); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSettingsStore(dir).Load(); err != nil {
		t.Fatal(err)
	}
}
