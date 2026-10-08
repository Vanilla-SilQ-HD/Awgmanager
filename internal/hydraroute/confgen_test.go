package hydraroute

import (
	"reflect"
	"strings"
	"testing"
)

func TestGenerateDomainConf_Basic(t *testing.T) {
	lists := []ManagedEntry{
		{
			ListName: "Telegram",
			Domains:  []string{"t.me", "telegram.org"},
			Iface:    "Wireguard0",
		},
	}
	got := GenerateDomainConf(lists)

	mustContain(t, got, "## Telegram")
	mustContain(t, got, "t.me,telegram.org/Wireguard0")

	if strings.Contains(got, "list:") {
		t.Errorf("marker must not contain legacy 'list:' prefix; got:\n%s", got)
	}
	if strings.Contains(got, "{") {
		t.Errorf("marker must not contain legacy {uuid}; got:\n%s", got)
	}
}

func TestGenerateDomainConf_GeoSiteTags(t *testing.T) {
	lists := []ManagedEntry{
		{
			ListName: "Google",
			Domains:  []string{"google.com", "geosite:GOOGLE"},
			Iface:    "Wireguard1",
		},
	}
	got := GenerateDomainConf(lists)

	mustContain(t, got, "## Google")
	mustContain(t, got, "google.com,geosite:GOOGLE/Wireguard1")
}

func TestGenerateDomainConf_Empty(t *testing.T) {
	got := GenerateDomainConf(nil)
	if got != "" {
		t.Errorf("expected empty output, got: %q", got)
	}
}

func TestGenerateIPList_Basic(t *testing.T) {
	lists := []ManagedEntry{
		{
			ListName: "Telegram",
			Subnets:  []string{"91.108.4.0/22", "149.154.160.0/20"},
			Iface:    "Wireguard0",
		},
	}
	got := GenerateIPList(lists, nil)

	mustContain(t, got, "## Telegram")
	mustContain(t, got, "/Wireguard0")
	mustContain(t, got, "91.108.4.0/22")
	mustContain(t, got, "149.154.160.0/20")
}

func TestGenerateIPList_GeoIPTag(t *testing.T) {
	lists := []ManagedEntry{
		{
			ListName: "Russia",
			Subnets:  []string{"5.8.0.0/21", "geoip:RU"},
			Iface:    "Wireguard2",
		},
	}
	got := GenerateIPList(lists, nil)

	mustContain(t, got, "## Russia")
	mustContain(t, got, "/Wireguard2")
	mustContain(t, got, "5.8.0.0/21")
	mustContain(t, got, "geoip:RU")
}

// TestGenerateIPList_DisabledUsesHRNeoFormat — #1022: выключенный блок —
// `#/Target`, а подсети без `#`. Для HR Neo строка `#10.0.0.0/8` не
// комментарий, а неверный CIDR, и HRweb из-за неё не сохраняет ни одно правило.
func TestGenerateIPList_DisabledUsesHRNeoFormat(t *testing.T) {
	got := GenerateIPList([]ManagedEntry{
		{ListName: "Off", Subnets: []string{"10.0.0.0/8", "geoip:RU"}, Iface: "nwg0", Disabled: true},
		{ListName: "On", Subnets: []string{"91.108.4.0/22"}, Iface: "nwg1"},
	}, nil)
	want := "## Off\n#/nwg0\n10.0.0.0/8\ngeoip:RU\n\n" +
		"## On\n/nwg1\n91.108.4.0/22\n\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestGenerateIPList_OversizedSection — #1025: служебный раздел HR Neo
// пишется в его формате: после пустой строки, теги строчными, без повторов.
func TestGenerateIPList_OversizedSection(t *testing.T) {
	got := GenerateIPList([]ManagedEntry{
		{ListName: "On", Subnets: []string{"91.108.4.0/22"}, Iface: "nwg1"},
	}, []string{"geoip:RU-blocked", "geoip:ru-blocked", "geoip:cn"})
	want := "## On\n/nwg1\n91.108.4.0/22\n\n" +
		"##impossible to use\n#/Too-big-geoip-tag\ngeoip:ru-blocked\ngeoip:cn\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	if got := GenerateIPList(nil, []string{"geoip:cn"}); got != "##impossible to use\n#/Too-big-geoip-tag\ngeoip:cn\n" {
		t.Errorf("only oversized: %q", got)
	}
	if got := GenerateIPList(nil, nil); got != "" {
		t.Errorf("nothing to write: %q", got)
	}

	// Чтение возвращает те же правила и теги.
	entries, oversized := parseIPList(GenerateIPList([]ManagedEntry{
		{ListName: "Off", Subnets: []string{"10.0.0.0/8"}, Iface: "nwg0", Disabled: true},
	}, []string{"geoip:cn"}))
	if len(entries) != 1 || !entries[0].Disabled || !reflect.DeepEqual(oversized, []string{"geoip:cn"}) {
		t.Errorf("roundtrip: entries=%+v oversized=%v", entries, oversized)
	}
}

func mustContain(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected output to contain %q\nfull output:\n%s", substr, s)
	}
}
