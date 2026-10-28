package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestQuarantineCorrupt_PreservesCopies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")
	SetNoticeSink(func(Notice) {})
	var notices []Notice
	SetNoticeSink(func(n Notice) { notices = append(notices, n) })
	t.Cleanup(func() { SetNoticeSink(nil) })
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("broken-%d", i)), 0600); err != nil {
			t.Fatal(err)
		}
		QuarantineCorrupt(path, fmt.Errorf("bad JSON"))
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("source still exists: %v", err)
		}
		for j := 0; j <= i; j++ {
			name := path + ".corrupt"
			if j > 0 {
				name += fmt.Sprintf(".%d", j)
			}
			assertQuarantine(t, name, fmt.Sprintf("broken-%d", j))
		}
	}
	if len(notices) != 3 || !strings.Contains(notices[2].Message, "secret.json.corrupt.2") {
		t.Fatalf("notice does not name actual copy: %+v", notices)
	}
}

func assertQuarantine(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("%s: content %q, error %v; want %q", path, data, err, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("%s: permissions %o, want 0600", path, info.Mode().Perm())
	}
}

func TestQuarantineCorrupt_Concurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(path+".corrupt", []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			QuarantineCorrupt(path, fmt.Errorf("bad JSON"))
		}()
	}
	wg.Wait()
	assertQuarantine(t, path+".corrupt", "previous")
	assertQuarantine(t, path+".corrupt.1", "current")
}

func TestQuarantineFile_InterruptedAfterLink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(path, []byte("complete"), 0600); err != nil {
		t.Fatal(err)
	}
	// Simulate a process stopping after link but before unlink.
	if err := os.Link(path, path+".corrupt"); err != nil {
		t.Fatal(err)
	}
	name, err := quarantineFile(path)
	if err != nil || name != path+".corrupt.1" {
		t.Fatalf("quarantineFile = %q, %v", name, err)
	}
	assertQuarantine(t, path+".corrupt", "complete")
	assertQuarantine(t, name, "complete")
}

func TestQuarantineFile_Permissions(t *testing.T) {
	for _, mode := range []os.FileMode{0644, 0600, 0400} {
		t.Run(fmt.Sprintf("%o", mode), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secret.json")
			if err := os.WriteFile(path, []byte("secret"), mode); err != nil {
				t.Fatal(err)
			}
			name, err := quarantineFile(path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(name)
			if err != nil || info.Mode().Perm() != mode&0600 {
				t.Fatalf("permissions not preserved/tightened: info=%v err=%v", info, err)
			}
		})
	}
}

func TestQuarantineFile_SymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.json")
	target := filepath.Join(dir, "previous")
	if err := os.WriteFile(target, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+".corrupt"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	name, err := quarantineFile(path)
	if err != nil || name != path+".corrupt.1" {
		t.Fatalf("quarantineFile = %q, %v", name, err)
	}
	assertQuarantine(t, target, "previous")
	assertQuarantine(t, name, "current")
}

func TestQuarantineCorrupt_FailureNotice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(path, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".corrupt", 0700); err != nil {
		t.Fatal(err)
	}
	SetNoticeSink(func(Notice) {})
	var notices []Notice
	SetNoticeSink(func(n Notice) { notices = append(notices, n) })
	t.Cleanup(func() { SetNoticeSink(nil) })
	QuarantineCorrupt(path, fmt.Errorf("bad JSON"))
	assertQuarantine(t, path, "current")
	if len(notices) != 1 || !strings.Contains(notices[0].Message, "не поддаётся") ||
		strings.Contains(notices[0].Message, "копия сохранена") {
		t.Fatalf("failure was not reported: %+v", notices)
	}
}

func TestQuarantineFile_FailuresLeaveSource(t *testing.T) {
	for _, kind := range []string{"missing", "directory", "symlink", "unwritable directory", "unreadable file"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "secret.json")
			switch kind {
			case "missing":
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("missing-target", path); err != nil {
					t.Fatal(err)
				}
			default:
				if os.Geteuid() == 0 {
					t.Skip("root bypasses permission checks")
				}
				if err := os.WriteFile(path, []byte("current"), 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "unwritable directory" {
					if err := os.Chmod(dir, 0500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
				} else {
					if err := os.Chmod(path, 0000); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(path, 0600) })
				}
			}
			if name, err := quarantineFile(path); err == nil || name != "" {
				t.Fatalf("unexpected success: %q, %v", name, err)
			}
			if kind != "missing" {
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("source lost: %v", err)
				}
			}
			matches, err := filepath.Glob(path + ".corrupt*")
			if err != nil || len(matches) != 0 {
				t.Fatalf("unexpected copies: %v, %v", matches, err)
			}
		})
	}
}
