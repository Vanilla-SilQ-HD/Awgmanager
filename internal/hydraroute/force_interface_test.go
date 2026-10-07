package hydraroute

import (
	"os"
	"testing"
)

func readTestConf(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(hrConfPath)
	if err != nil {
		t.Fatalf("read conf: %v", err)
	}
	return string(data)
}

func entriesWith(ifaces ...string) map[string]ManagedEntry {
	m := map[string]ManagedEntry{}
	for i, iface := range ifaces {
		name := string(rune('a' + i))
		m[name] = ManagedEntry{ListName: name, Iface: iface}
	}
	return m
}

// Шаблон пишется только для семейств, которые реально стоят целью,
// а не для всех трёх; чужая цель (политика, сторонний интерфейс) не в счёт.
func TestSyncForceInterface_AddsUsedFamiliesOnly(t *testing.T) {
	setupTestConf(t, "log=off\nDirectRouteEnabled=true\n")
	changed, err := syncForceInterface(entriesWith("awgm0", "opkgtun3", "awgm12", "HydraRoute", "nwg0", "t2s"))
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	want := "log=off\nDirectRouteEnabled=true\nForceInterface=awgmX,opkgtunX\n"
	if got := readTestConf(t); got != want {
		t.Fatalf("conf:\n%s\nwant:\n%s", got, want)
	}
}

// Записи пользователя и их порядок сохраняются, наше дописывается в хвост.
func TestSyncForceInterface_KeepsUserEntries(t *testing.T) {
	setupTestConf(t, "forceinterface=wg9, t2sX\nlog=off\n")
	changed, err := syncForceInterface(entriesWith("t2s1", "awgm0"))
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	want := "forceinterface=wg9,t2sX,awgmX\nlog=off\n"
	if got := readTestConf(t); got != want {
		t.Fatalf("conf:\n%s\nwant:\n%s", got, want)
	}
}

// HR Neo складывает несколько строк ключа: при сведении в одну значения
// второй строки не теряются.
func TestSyncForceInterface_MergesDuplicateLines(t *testing.T) {
	setupTestConf(t, "ForceInterface=wg9\nlog=off\nForceInterface=awgmX,tun7\n")
	changed, err := syncForceInterface(entriesWith("awgm0"))
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	want := "ForceInterface=wg9,awgmX,tun7\nlog=off\n"
	if got := readTestConf(t); got != want {
		t.Fatalf("conf:\n%s\nwant:\n%s", got, want)
	}
}

// Всё нужное уже есть — файл не трогается.
func TestSyncForceInterface_NoChangeNoWrite(t *testing.T) {
	setupTestConf(t, "ForceInterface=opkgtunX,awgmX\n")
	before, err := os.Stat(hrConfPath)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := syncForceInterface(entriesWith("awgm0", "opkgtun1"))
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	after, err := os.Stat(hrConfPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || readTestConf(t) != "ForceInterface=opkgtunX,awgmX\n" {
		t.Fatal("conf rewritten without change")
	}

	changed, err = syncForceInterface(entriesWith("HydraRoute"))
	if err != nil || changed {
		t.Fatalf("no own targets: changed=%v err=%v", changed, err)
	}
}

// Выключенное правило HR Neo не читает — его цель шаблон не добавляет.
func TestSyncForceInterface_IgnoresDisabledRules(t *testing.T) {
	setupTestConf(t, "log=off\n")
	entries := map[string]ManagedEntry{
		"on":  {ListName: "on", Iface: "awgm0"},
		"off": {ListName: "off", Iface: "opkgtun0", Disabled: true},
	}
	changed, err := syncForceInterface(entries)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if got := readTestConf(t); got != "log=off\nForceInterface=awgmX\n" {
		t.Fatalf("conf = %q", got)
	}
}

// Правило из панели с целью-нашим интерфейсом сразу попадает в ForceInterface:
// без этого HR Neo на следующем старте снова завёл бы пустую политику.
func TestCreateRule_SyncsForceInterface(t *testing.T) {
	svc, _, _ := setupRuleFiles(t)
	if _, err := svc.CreateRule(HRRule{Name: "NL", Domains: []string{"example.com"}, Target: "awgm0"}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if got := readTestConf(t); got != "ForceInterface=awgmX\n" {
		t.Fatalf("conf = %q", got)
	}
}
