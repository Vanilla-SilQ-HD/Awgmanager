package hydraroute

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOversizedTags_EnrichesWithCounts(t *testing.T) {
	svc, _, ipPath := setupRuleFiles(t)
	svc.geodata = setupGeoDataWithTags(t, map[string][]GeoTag{
		"ru-blocked": {{Name: "ru-blocked", Count: 82411}},
		"cn-heavy":   {{Name: "cn-heavy", Count: 128953}},
	})

	if err := os.WriteFile(ipPath, []byte(
		"##impossible to use\n"+
			"#/Too-big-geoip-tag\n"+
			"geoip:ru-blocked\n"+
			"geoip:cn-heavy\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := svc.OversizedTags(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	want := []OversizedTag{
		{Name: "geoip:ru-blocked", Count: 82411, File: svc.geodata.entries[0].Path},
		{Name: "geoip:cn-heavy", Count: 128953, File: svc.geodata.entries[0].Path},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestOversizedTags_UnknownTagHasNegativeCount(t *testing.T) {
	svc, _, ipPath := setupRuleFiles(t)
	svc.geodata = setupGeoDataWithTags(t, map[string][]GeoTag{
		"ru-blocked": {{Name: "ru-blocked", Count: 82411}},
	})

	if err := os.WriteFile(ipPath, []byte(
		"##impossible to use\n"+
			"#/Too-big-geoip-tag\n"+
			"geoip:gone-from-dat\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := svc.OversizedTags(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "geoip:gone-from-dat" || got[0].Count != -1 {
		t.Errorf("want one entry with count=-1, got %+v", got)
	}
}

// setupGeoDataWithTags stages an in-memory GeoDataStore with a single
// geoip file whose tag cache is pre-populated from the provided map.
func setupGeoDataWithTags(t *testing.T, tagsByName map[string][]GeoTag) *GeoDataStore {
	t.Helper()
	dir := t.TempDir()

	gds := NewGeoDataStore(dir)
	path := filepath.Join(gds.geoDir, "geoip.dat")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	gds.entries = []GeoFileEntry{{Type: "geoip", Path: path}}
	all := make([]GeoTag, 0, len(tagsByName))
	for _, v := range tagsByName {
		all = append(all, v...)
	}
	gds.tagCache[path] = all
	return gds
}

func TestRemoveOversizedTag(t *testing.T) {
	const rule = "## 2ip\n/HydraRoute\ngeoip:RU\n\n"
	const seed = rule +
		"##impossible to use\n#/Too-big-geoip-tag\ngeoip:ru-blocked\ngeoip:cn\n"

	t.Run("one of several", func(t *testing.T) {
		svc, domainPath, ipPath := setupRuleFiles(t)
		if err := os.WriteFile(ipPath, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		// Регистр и префикс geoip: не важны — как HR Neo сравнивает теги.
		if err := svc.RemoveOversizedTag(" RU-Blocked "); err != nil {
			t.Fatal(err)
		}
		want := rule + "##impossible to use\n#/Too-big-geoip-tag\ngeoip:cn\n"
		if got := readFileOrEmpty(t, ipPath); got != want {
			t.Errorf("ip.list:\n%s\nwant:\n%s", got, want)
		}
		if _, err := os.Stat(domainPath); !os.IsNotExist(err) {
			t.Errorf("domain.conf must not be written, stat err = %v", err)
		}
		svc.mu.Lock()
		restart := svc.restartTimer != nil
		svc.mu.Unlock()
		if restart {
			t.Error("HR Neo restart scheduled, want none: rules did not change")
		}
	})

	t.Run("last tag drops the section", func(t *testing.T) {
		svc, _, ipPath := setupRuleFiles(t)
		if err := os.WriteFile(ipPath, []byte(rule+"##impossible to use\n#/Too-big-geoip-tag\ngeoip:cn\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := svc.RemoveOversizedTag("geoip:cn"); err != nil {
			t.Fatal(err)
		}
		if got := readFileOrEmpty(t, ipPath); got != rule {
			t.Errorf("ip.list:\n%s\nwant:\n%s", got, rule)
		}
	})

	t.Run("not found", func(t *testing.T) {
		svc, _, ipPath := setupRuleFiles(t)
		if err := os.WriteFile(ipPath, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := svc.RemoveOversizedTag("geoip:ru"); !errors.Is(err, ErrOversizedTagNotFound) {
			t.Fatalf("err = %v, want ErrOversizedTagNotFound", err)
		}
		if got := readFileOrEmpty(t, ipPath); got != seed {
			t.Errorf("ip.list changed:\n%s", got)
		}
	})

	t.Run("empty name", func(t *testing.T) {
		svc, _, _ := setupRuleFiles(t)
		if err := svc.RemoveOversizedTag(" geoip: "); err == nil {
			t.Fatal("expected error for empty name")
		}
	})
}
