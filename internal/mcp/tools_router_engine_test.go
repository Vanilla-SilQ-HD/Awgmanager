package mcp_test

import (
	"strings"
	"testing"
)

func TestTools_SetSingboxRouterMode(t *testing.T) {
	s, f := newTestSession(t)
	res, out := callTool(t, s, "set_singbox_router_mode", map[string]any{"mode": " FakeIP-Tun "})
	if res.IsError || out["started"] != true || f.RoutingMode != "fakeip-tun" || !strings.Contains(toolText(res), "not done yet") {
		t.Fatalf("%s / %v", toolText(res), out)
	}
	if res, _ := callTool(t, s, "set_singbox_router_mode", map[string]any{"mode": "tun"}); !res.IsError {
		t.Error("an unknown mode must be refused")
	}
}

func TestTools_SetSingboxRouterSettings(t *testing.T) {
	s, _ := newTestSession(t)
	res, out := callTool(t, s, "set_singbox_router_settings", map[string]any{"sniffer": false, "udpTimeout": "10m"})
	if res.IsError || out["sniffer"] != false || out["udpTimeout"] != "10m" {
		t.Fatalf("%s / %v", toolText(res), out)
	}
	for name, args := range map[string]map[string]any{
		"nothing":    {},
		"newline":    {"bypassPorts": "80\n443"},
		"bad policy": {"policyName": "../x"},
	} {
		if res, _ := callTool(t, s, "set_singbox_router_settings", args); !res.IsError {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestTools_SingboxUserConfig(t *testing.T) {
	s, _ := newTestSession(t)
	_, read := callTool(t, s, "get_singbox_user_config", nil)
	if read["revision"] == "" {
		t.Fatalf("out = %v", read)
	}
	if res, _ := callTool(t, s, "save_singbox_user_config", map[string]any{"content": `{"log":{}}`}); !res.IsError || !strings.Contains(toolText(res), "expectRevision") {
		t.Fatalf("a save without a revision must be refused: %s", toolText(res))
	}
	res, out := callTool(t, s, "save_singbox_user_config", map[string]any{"content": `{"outbounds":[{"tag":"broken"}]}`, "expectRevision": read["revision"]})
	if res.IsError || out["ok"] != false || !strings.Contains(toolText(res), "FAILED") || out["revision"] == "" {
		t.Fatalf("%s / %v", toolText(res), out)
	}
	if _, out := callTool(t, s, "apply_singbox_user_config", map[string]any{"expectRevision": out["revision"]}); out["ok"] != false {
		t.Fatalf("a failing draft must not apply: %v", out)
	}
	// A revision read before another save names nothing now.
	if res, _ := callTool(t, s, "save_singbox_user_config", map[string]any{"content": `{"log":{}}`, "expectRevision": read["revision"]}); !res.IsError || !strings.Contains(toolText(res), "get_singbox_user_config") {
		t.Fatalf("a stale revision must be refused: %s", toolText(res))
	}
	_, read = callTool(t, s, "get_singbox_user_config", nil)
	if read["hasDraft"] != true {
		t.Fatalf("out = %v", read)
	}
	res, saved := callTool(t, s, "save_singbox_user_config", map[string]any{"content": `{"log":{"level":"warn"}}`, "expectRevision": read["revision"]})
	if res.IsError || saved["ok"] != true {
		t.Fatalf("the current revision saves: %s", toolText(res))
	}
	if res, _ := callTool(t, s, "apply_singbox_user_config", nil); !res.IsError || !strings.Contains(toolText(res), "expectRevision") {
		t.Fatalf("an apply without a revision must be refused: %s", toolText(res))
	}
	if res, _ := callTool(t, s, "apply_singbox_user_config", map[string]any{"expectRevision": read["revision"]}); !res.IsError {
		t.Fatal("an apply naming another draft must be refused")
	}
	if res, out := callTool(t, s, "apply_singbox_user_config", map[string]any{"expectRevision": saved["revision"]}); res.IsError || out["ok"] != true {
		t.Fatalf("%s / %v", toolText(res), out)
	}
	if res, out := callTool(t, s, "discard_singbox_user_config", nil); res.IsError || out["discarded"] != true {
		t.Fatalf("%s / %v", toolText(res), out)
	}
	for _, c := range []string{"", "[1,2]", "{not json", "null"} {
		if res, _ := callTool(t, s, "save_singbox_user_config", map[string]any{"content": c, "expectRevision": "x"}); !res.IsError {
			t.Errorf("%q must be refused", c)
		}
	}
}

// TestScope_ReadOnlyKeyAndRouterEngine — 90-user.json может держать учётные
// данные: ключу «только чтение» он не отдаётся, как конфиг туннеля; смена
// режима и правки движка — запись.
func TestScope_ReadOnlyKeyAndRouterEngine(t *testing.T) {
	s := scopedSession(t, true)
	for name, args := range map[string]map[string]any{
		"get_singbox_user_config":     nil,
		"set_singbox_router_mode":     {"mode": "off"},
		"set_singbox_router_settings": {"sniffer": true},
		"save_singbox_user_config":    {"content": "{}"},
		"apply_singbox_user_config":   nil,
		"discard_singbox_user_config": nil,
	} {
		res, _ := callTool(t, s, name, args)
		if !res.IsError || !strings.Contains(strings.ToLower(toolText(res)), "read-only") {
			t.Errorf("%s: a read-only key must be refused: %q", name, toolText(res))
		}
	}
}
