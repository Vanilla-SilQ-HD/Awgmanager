package localdeps

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	mcpsrv "github.com/hoaxisr/awg-manager/internal/mcp"
	singboxorch "github.com/hoaxisr/awg-manager/internal/singbox/orchestrator"
	"github.com/hoaxisr/awg-manager/internal/storage"
)

// fakeSlots is the 90-user.json slot of the orchestrator.
type fakeSlots struct {
	effective []byte
	draft     []byte
	enabled   bool
	checkRes  singboxorch.ValidationResult
	checkErr  error
	applyRes  singboxorch.ValidationResult
	applyErr  error
	enableErr error
	enabledTo []bool
	// beforeWrite runs inside save and apply before the check: the web
	// editor saving its own draft in between.
	beforeWrite func(*fakeSlots)
}

func (f *fakeSlots) Snapshot() []singboxorch.SlotState {
	return []singboxorch.SlotState{{Slot: singboxorch.SlotUser, Enabled: f.enabled}}
}
func (f *fakeSlots) LoadEffective(singboxorch.Slot) ([]byte, error) {
	if f.draft != nil {
		return f.draft, nil
	}
	return f.effective, nil
}
func (f *fakeSlots) HasDraft(singboxorch.Slot) bool { return f.draft != nil }

// SaveDraftChecked mirrors the orchestrator: check sees the effective
// bytes before the write; beforeWrite is another save landing first.
func (f *fakeSlots) SaveDraftChecked(s singboxorch.Slot, b []byte, check func([]byte) error) error {
	if f.beforeWrite != nil {
		f.beforeWrite(f)
	}
	if check != nil {
		cur, _ := f.LoadEffective(s)
		if err := check(cur); err != nil {
			return err
		}
	}
	f.draft = b
	return nil
}
func (f *fakeSlots) CheckMerged(singboxorch.Slot, []byte) (singboxorch.ValidationResult, error) {
	return f.checkRes, f.checkErr
}
func (f *fakeSlots) ApplyDraftChecked(_ singboxorch.Slot, check func([]byte) error) (singboxorch.ValidationResult, error) {
	if f.beforeWrite != nil {
		f.beforeWrite(f)
	}
	if f.draft == nil {
		return singboxorch.ValidationResult{}, singboxorch.ErrNoDraft
	}
	if check != nil {
		if err := check(f.draft); err != nil {
			return singboxorch.ValidationResult{}, err
		}
	}
	return f.applyRes, f.applyErr
}
func (f *fakeSlots) DiscardDraft(singboxorch.Slot) error { f.draft = nil; return nil }
func (f *fakeSlots) SetEnabled(_ singboxorch.Slot, on bool) error {
	f.enabledTo = append(f.enabledTo, on)
	return f.enableErr
}

// TestLocal_UpdateSingboxRouterSettingsKeepsTheRest — UpdateSettings берёт
// структуру целиком: поля, которых не касались, обязаны дойти такими, какие
// они сохранены.
func TestLocal_UpdateSingboxRouterSettingsKeepsTheRest(t *testing.T) {
	fr := routerHarness()
	fr.settings = storage.SingboxRouterSettings{Enabled: true, RoutingMode: "tproxy", PolicyName: "Policy1", UDPTimeout: "5m", BypassGeoIPTags: []string{"ru"}, FakeIPPool6: "fd00::/8"}
	l := New(Config{Router: fr})

	if _, err := l.UpdateSingboxRouterSettings(context.Background(), mcpsrv.SingboxRouterSettingsPatch{Sniffer: ptr(true), BypassPorts: ptr(" 5060 ")}); err != nil {
		t.Fatal(err)
	}
	s := fr.settings
	if !s.SnifferEnabled || s.BypassExtraPorts != "5060" || s.PolicyName != "Policy1" || s.UDPTimeout != "5m" || s.RoutingMode != "tproxy" || len(s.BypassGeoIPTags) != 1 || s.FakeIPPool6 != "fd00::/8" {
		t.Fatalf("settings = %+v", s)
	}
	if _, err := l.UpdateSingboxRouterSettings(context.Background(), mcpsrv.SingboxRouterSettingsPatch{
		UDPTimeout: ptr(" 10m "), BypassSubnets: ptr(" 10.0.0.0/8 "), PolicyName: ptr(" Policy2 "), DeviceMode: ptr(" all "),
	}); err != nil {
		t.Fatal(err)
	}
	s = fr.settings
	if s.UDPTimeout != "10m" || s.BypassExtraSubnets != "10.0.0.0/8" || s.PolicyName != "Policy2" || s.DeviceMode != "all" || !s.SnifferEnabled || s.BypassExtraPorts != "5060" {
		t.Fatalf("every field of the patch lands, trimmed, and the rest stays: %+v", s)
	}
}

func TestLocal_SwitchSingboxRouterModeRunsInTheBackground(t *testing.T) {
	fr := routerHarness()
	fr.switched = make(chan string, 1)
	l := New(Config{Router: fr})
	if err := l.SwitchSingboxRouterMode(context.Background(), "fakeip-tun"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-fr.switched:
		if got != "fakeip-tun" {
			t.Fatalf("mode = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the switch was not started")
	}
}

// TestLocal_SingboxUserConfig — черновик сохраняется и проверяется;
// применение с провалом проверки ничего не включает; удачное применение
// включает слот после apply, как редактор. Ревизия обязательна и у
// сохранения, и у применения.
func TestLocal_SingboxUserConfig(t *testing.T) {
	fs := &fakeSlots{effective: []byte(`{"outbounds":[]}`), enabled: true}
	l := New(Config{UserConfig: fs})
	ctx := context.Background()

	got, err := l.GetSingboxUserConfig(ctx)
	if err != nil || got.Content != `{"outbounds":[]}` || got.HasDraft || !got.Enabled || got.Size != 16 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := l.SaveSingboxUserConfig(ctx, []byte(`{}`), ""); err == nil || fs.draft != nil {
		t.Fatalf("a save without a revision must be refused: %v", err)
	}
	fs.checkRes = singboxorch.ValidationResult{Errors: []singboxorch.ValidationError{{Kind: "unknown-outbound", Message: "unknown outbound x"}, {Severity: singboxorch.SeverityWarning, Message: "dns final"}}}
	check, err := l.SaveSingboxUserConfig(ctx, []byte(`{"route":{"final":"x"}}`), got.Revision)
	if err != nil || check.Ok || len(check.Errors) != 1 || len(check.Warnings) != 1 || string(fs.draft) != `{"route":{"final":"x"}}` || check.Revision != userConfigRevision(fs.draft) {
		t.Fatalf("check = %+v, %v", check, err)
	}
	rev := check.Revision

	if _, err := l.ApplySingboxUserConfig(ctx, ""); err == nil {
		t.Fatal("an apply without a revision must be refused")
	}
	fs.applyRes = fs.checkRes
	check, err = l.ApplySingboxUserConfig(ctx, rev)
	if err != nil || check.Ok || len(fs.enabledTo) != 0 {
		t.Fatalf("a refused apply must enable nothing: %+v, %v, %v", check, err, fs.enabledTo)
	}
	fs.applyRes, fs.applyErr = singboxorch.ValidationResult{}, fmt.Errorf("%w: FATAL", singboxorch.ErrSingboxCheck)
	if check, err = l.ApplySingboxUserConfig(ctx, rev); err != nil || check.Ok || !strings.Contains(check.Errors[0], "FATAL") {
		t.Fatalf("a sing-box refusal is a failed check: %+v, %v", check, err)
	}
	fs.applyErr = errors.New("ApplyDraft tmpdir: no space left on device")
	if check, err = l.ApplySingboxUserConfig(ctx, rev); err == nil || !strings.Contains(err.Error(), "not a verdict") || check.Ok || len(check.Errors) != 0 {
		t.Fatalf("a file system failure is an error, not a failed check the agent would fix in the JSON: %+v, %v", check, err)
	}
	fs.applyErr = nil
	if check, err = l.ApplySingboxUserConfig(ctx, rev); err != nil || !check.Ok || len(fs.enabledTo) != 1 || !fs.enabledTo[0] || len(check.Warnings) != 0 {
		t.Fatalf("a good apply enables the slot: %+v, %v, %v", check, err, fs.enabledTo)
	}
	// A rule mixing a rule_set with its own addresses is flagged on save and
	// on apply, as the editor's check and apply do (#699).
	mixed := []byte(`{"route":{"rules":[{"rule_set":["geosite-x"],"ip_cidr":["10.0.0.0/8"],"outbound":"vpn"}]}}`)
	fs.checkRes = singboxorch.ValidationResult{}
	if check, err = l.SaveSingboxUserConfig(ctx, mixed, rev); err != nil || len(check.Warnings) != 1 || !strings.Contains(check.Warnings[0], "route.rules[0]") {
		t.Fatalf("save must flag the rule: %+v, %v", check, err)
	}
	rev = check.Revision
	if check, err = l.ApplySingboxUserConfig(ctx, rev); err != nil || len(check.Warnings) != 1 {
		t.Fatalf("apply must flag the rule of the draft it applied: %+v, %v", check, err)
	}
	fs.enableErr = errors.New("slot busy")
	if _, err = l.ApplySingboxUserConfig(ctx, rev); err == nil || !strings.Contains(err.Error(), "could not be enabled") {
		t.Fatalf("applied but not enabled must say so: %v", err)
	}
	fs.enableErr, fs.checkErr = nil, errors.New("CheckMerged tmpdir: no space left on device")
	if check, err = l.SaveSingboxUserConfig(ctx, []byte(`{}`), rev); err == nil || !strings.Contains(err.Error(), "could not run") || check.Ok || len(check.Errors) != 0 || string(fs.draft) != `{}` {
		t.Fatalf("a check that could not run is an error about the router, not a verdict on the JSON: %+v, %v", check, err)
	}
	fs.checkErr = nil
	if err := l.DiscardSingboxUserConfig(ctx); err != nil || fs.draft != nil {
		t.Fatal("discard must drop the draft")
	}
	if _, err := l.ApplySingboxUserConfig(ctx, rev); err == nil {
		t.Fatal("no draft, nothing to apply")
	}

	fs.effective = []byte(strings.Repeat("x", maxUserConfigOut+10))
	if got, _ := l.GetSingboxUserConfig(ctx); !got.Truncated || len(got.Content) != maxUserConfigOut || got.Revision != "" || got.Size != maxUserConfigOut+10 {
		t.Fatalf("a long slot is cut, says so, and has no revision to save the cut copy with: %d %v %q", len(got.Content), got.Truncated, got.Revision)
	}
}

// TestLocal_SingboxUserConfigChecksTheRevision — черновик общий с
// веб-редактором. Сохранение после чужого сохранения не перезаписывает его,
// а применение не применяет чужой черновик: сверка идёт в той же операции
// orchestrator, что и запись.
func TestLocal_SingboxUserConfigChecksTheRevision(t *testing.T) {
	fs := &fakeSlots{effective: []byte(`{"outbounds":[]}`), enabled: true}
	l := New(Config{UserConfig: fs})
	ctx := context.Background()
	read, err := l.GetSingboxUserConfig(ctx)
	if err != nil || read.Revision == "" {
		t.Fatalf("read = %+v, %v", read, err)
	}
	fs.beforeWrite = func(f *fakeSlots) { f.draft = []byte(`{"log":{"level":"debug"}}`) }
	if _, err := l.SaveSingboxUserConfig(ctx, []byte(`{"route":{}}`), read.Revision); err == nil || !strings.Contains(err.Error(), "get_singbox_user_config") || string(fs.draft) != `{"log":{"level":"debug"}}` {
		t.Fatalf("a draft saved meanwhile must not be overwritten: %v, %s", err, fs.draft)
	}
	fs.beforeWrite = nil
	again, _ := l.GetSingboxUserConfig(ctx)
	saved, err := l.SaveSingboxUserConfig(ctx, []byte(`{"route":{}}`), again.Revision)
	if err != nil || string(fs.draft) != `{"route":{}}` {
		t.Fatalf("the current revision saves: %v, %s", err, fs.draft)
	}
	fs.beforeWrite = func(f *fakeSlots) { f.draft = []byte(`{"log":{"level":"warn"}}`) }
	if _, err := l.ApplySingboxUserConfig(ctx, saved.Revision); !errors.Is(err, errUserConfigChanged) || len(fs.enabledTo) != 0 {
		t.Fatalf("a draft the web editor saved since must not be applied in the agent's name: %v", err)
	}
}
