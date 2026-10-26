package localdeps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	mcpsrv "github.com/hoaxisr/awg-manager/internal/mcp"
	singboxorch "github.com/hoaxisr/awg-manager/internal/singbox/orchestrator"
	"github.com/hoaxisr/awg-manager/internal/singbox/router"
)

// maxUserConfigOut caps the expert slot returned to the model; the web
// editor shows it whole.
const maxUserConfigOut = 64 << 10

// SwitchSingboxRouterMode starts the switch in the background, as the REST
// handler does: it outlives the call (tens of seconds), and its terminal
// error is journalled here — the transition events reach no one when no
// tab is open.
func (l *Local) SwitchSingboxRouterMode(_ context.Context, mode string) error {
	if l.c.Router == nil {
		return errUnavailable("sing-box router")
	}
	l.singboxLog.Info("mode-switch", mode, "Routing mode switch requested (MCP): "+mode)
	go func() {
		if err := l.c.Router.SwitchRoutingMode(context.Background(), mode); err != nil {
			l.singboxLog.Warn("mode-switch", mode, "Routing mode switch failed (MCP): "+err.Error())
		}
	}()
	return nil
}

// UpdateSingboxRouterSettings reads the stored settings and lays the
// caller's fields over them: UpdateSettings takes the whole struct, and the
// REST handler likewise fills what the form did not send.
func (l *Local) UpdateSingboxRouterSettings(ctx context.Context, p mcpsrv.SingboxRouterSettingsPatch) (mcpsrv.SingboxRouterStatus, error) {
	if l.c.Router == nil {
		return mcpsrv.SingboxRouterStatus{}, errUnavailable("sing-box router")
	}
	sr, err := l.c.Router.GetSettings(ctx)
	if err != nil {
		return mcpsrv.SingboxRouterStatus{}, err
	}
	if p.Sniffer != nil {
		sr.SnifferEnabled = *p.Sniffer
	}
	if p.UDPTimeout != nil {
		sr.UDPTimeout = strings.TrimSpace(*p.UDPTimeout)
	}
	if p.BypassPorts != nil {
		sr.BypassExtraPorts = strings.TrimSpace(*p.BypassPorts)
	}
	if p.BypassSubnets != nil {
		sr.BypassExtraSubnets = strings.TrimSpace(*p.BypassSubnets)
	}
	if p.PolicyName != nil {
		sr.PolicyName = strings.TrimSpace(*p.PolicyName)
	}
	if p.DeviceMode != nil {
		sr.DeviceMode = strings.TrimSpace(*p.DeviceMode)
	}
	if err := l.c.Router.UpdateSettings(ctx, sr); err != nil {
		l.singboxLog.Warn("settings", "", "Failed to update sing-box router settings (MCP): "+err.Error())
		return mcpsrv.SingboxRouterStatus{}, err
	}
	l.singboxLog.Info("settings", "", "Sing-box router settings updated (MCP)")
	return l.SingboxRouterStatus(ctx)
}

func (l *Local) GetSingboxUserConfig(context.Context) (mcpsrv.SingboxUserConfig, error) {
	if l.c.UserConfig == nil {
		return mcpsrv.SingboxUserConfig{}, errUnavailable("the expert configuration")
	}
	raw, err := l.c.UserConfig.LoadEffective(singboxorch.SlotUser)
	if err != nil {
		return mcpsrv.SingboxUserConfig{}, err
	}
	out := mcpsrv.SingboxUserConfig{HasDraft: l.c.UserConfig.HasDraft(singboxorch.SlotUser), Revision: userConfigRevision(raw), Size: len(raw)}
	for _, st := range l.c.UserConfig.Snapshot() {
		if st.Slot == singboxorch.SlotUser {
			out.Enabled = st.Enabled
		}
	}
	if len(raw) > maxUserConfigOut {
		// No revision for a cut copy: save takes one only with the whole
		// file, and a cut copy saved back would drop the rest of the user's
		// configuration.
		raw, out.Truncated, out.Revision = raw[:maxUserConfigOut], true, ""
	}
	out.Content = string(raw)
	return out, nil
}

// errUserConfigChanged is a save or apply refused because the slot is not
// the one the agent read or saved.
var errUserConfigChanged = errors.New("the expert configuration changed since it was read")

// revisionCheck compares the slot's bytes with expectRevision inside the
// orchestrator's lock (SaveDraftChecked, ApplyDraftChecked).
func revisionCheck(expectRevision, what string) func([]byte) error {
	return func(current []byte) error {
		if got := userConfigRevision(current); got != expectRevision {
			return fmt.Errorf("%w: %s is revision %s, not %s — another draft was saved, perhaps in the web interface; nothing was %s: read it again with get_singbox_user_config", errUserConfigChanged, what, got, expectRevision, map[string]string{"the slot": "saved", "the draft": "applied"}[what])
		}
		return nil
	}
}

// configCheck maps the orchestrator's verdict.
func configCheck(res singboxorch.ValidationResult) mcpsrv.SingboxConfigCheck {
	out := mcpsrv.SingboxConfigCheck{Ok: res.Ok()}
	for _, e := range res.Errors {
		msg := sanitizeText(e.Message, maxMessageRunes)
		if e.Severity == singboxorch.SeverityWarning {
			out.Warnings = append(out.Warnings, msg)
		} else {
			out.Errors = append(out.Errors, msg)
		}
	}
	return out
}

// userConfigAdvisories mirrors the editor's addressOrWarnings: a rule
// mixing a rule_set with its own addresses, which sing-box ANDs (#699).
// The generated slots are rewritten; the expert slot is the user's text,
// so it is only flagged.
func userConfigAdvisories(raw []byte) []string {
	var out []string
	for _, idx := range router.FlatAddressOrRuleIndexes(raw) {
		out = append(out, fmt.Sprintf("route.rules[%d] mixes a rule_set with its own domain/ip_cidr: sing-box requires both at once unless the set holds exactly one address rule, so it matches almost nothing — split it into two rules or write logical(or)", idx))
	}
	return out
}

// SaveSingboxUserConfig writes the draft, then checks the merged
// configuration with it, as the editor's save and check do. sing-box's own
// refusal (a check error) is reported as a failed check, not a failed call:
// the draft is saved either way.
// SaveSingboxUserConfig replaces the slot's draft — one the web editor
// saved included. expectRevision, from GetSingboxUserConfig, is compared
// with the slot as it stands just before the write: a draft saved since
// the agent read the slot is refused rather than overwritten.
//
// A check that could not run (CheckMerged's error: a temporary directory,
// a snapshot) says nothing about the content, so it is an error — as the
// editor's 500 — not a failed check the agent would try to fix in the JSON.
func (l *Local) SaveSingboxUserConfig(_ context.Context, content []byte, expectRevision string) (mcpsrv.SingboxConfigCheck, error) {
	if l.c.UserConfig == nil {
		return mcpsrv.SingboxConfigCheck{}, errUnavailable("the expert configuration")
	}
	if err := l.c.UserConfig.SaveDraftChecked(singboxorch.SlotUser, content, revisionCheck(expectRevision, "the slot")); err != nil {
		l.singboxLog.Warn("user-config-save", "90-user.json", "Failed to save draft (MCP): "+err.Error())
		return mcpsrv.SingboxConfigCheck{}, err
	}
	l.singboxLog.Info("user-config-save", "90-user.json", "User config draft saved (MCP)")
	res, err := l.c.UserConfig.CheckMerged(singboxorch.SlotUser, content)
	if err != nil {
		l.singboxLog.Warn("user-config-check", "90-user.json", "Merged check could not run (MCP): "+err.Error())
		return mcpsrv.SingboxConfigCheck{}, fmt.Errorf("the draft is saved, but the check could not run (%s) — this is not a verdict on the content; apply_singbox_user_config checks it again", sanitizeText(err.Error(), maxMessageRunes))
	}
	out := configCheck(res)
	out.Warnings = append(out.Warnings, userConfigAdvisories(content)...)
	out.Revision = userConfigRevision(content)
	return out, nil
}

// ApplySingboxUserConfig applies, then enables the slot — the editor's
// order: a refused apply leaves a parked slot parked.
//
// expectRevision names the draft the agent means — save's revision, or
// get's when the draft is there already. ApplyDraftChecked compares it
// with the draft it is about to commit, under the commit's lock, so a draft
// the web editor saved meanwhile is refused, not applied in the agent's
// name. A sing-box refusal (ErrSingboxCheck) is a failed check; any other
// error is the file system's, says nothing about the draft, and is an
// error — the agent must not start fixing the JSON for it.
func (l *Local) ApplySingboxUserConfig(_ context.Context, expectRevision string) (mcpsrv.SingboxConfigCheck, error) {
	if l.c.UserConfig == nil {
		return mcpsrv.SingboxConfigCheck{}, errUnavailable("the expert configuration")
	}
	if !l.c.UserConfig.HasDraft(singboxorch.SlotUser) {
		return mcpsrv.SingboxConfigCheck{}, fmt.Errorf("there is no draft to apply; save one with save_singbox_user_config")
	}
	// The draft is kept as the commit sees it, as the editor does: the
	// commit removes it, and its advisories go back with the result.
	var draft []byte
	check := revisionCheck(expectRevision, "the draft")
	res, err := l.c.UserConfig.ApplyDraftChecked(singboxorch.SlotUser, func(d []byte) error {
		draft = d
		return check(d)
	})
	if errors.Is(err, singboxorch.ErrNoDraft) {
		return mcpsrv.SingboxConfigCheck{}, fmt.Errorf("there is no draft to apply")
	}
	if errors.Is(err, errUserConfigChanged) {
		return mcpsrv.SingboxConfigCheck{}, err
	}
	if errors.Is(err, singboxorch.ErrSingboxCheck) {
		l.singboxLog.Warn("user-config-apply", "90-user.json", "Apply refused by sing-box check (MCP): "+err.Error())
		return mcpsrv.SingboxConfigCheck{Ok: false, Errors: []string{sanitizeText(err.Error(), maxMessageRunes)}}, nil
	}
	if err != nil {
		l.singboxLog.Warn("user-config-apply", "90-user.json", "Apply could not run (MCP): "+err.Error())
		return mcpsrv.SingboxConfigCheck{}, fmt.Errorf("the draft was not applied: applying could not run (%s) — this is the router's file system, not a verdict on the draft; nothing changed", sanitizeText(err.Error(), maxMessageRunes))
	}
	if !res.Ok() {
		l.singboxLog.Warn("user-config-apply", "90-user.json", "Apply refused: validation failed (MCP)")
		return configCheck(res), nil
	}
	if err := l.c.UserConfig.SetEnabled(singboxorch.SlotUser, true); err != nil {
		return mcpsrv.SingboxConfigCheck{}, fmt.Errorf("the configuration is applied, but the slot could not be enabled: %w", err)
	}
	l.singboxLog.Info("user-config-apply", "90-user.json", "User config draft applied (MCP)")
	out := configCheck(res)
	out.Warnings = append(out.Warnings, userConfigAdvisories(draft)...)
	return out, nil
}

func (l *Local) DiscardSingboxUserConfig(context.Context) error {
	if l.c.UserConfig == nil {
		return errUnavailable("the expert configuration")
	}
	if err := l.c.UserConfig.DiscardDraft(singboxorch.SlotUser); err != nil {
		return err
	}
	l.singboxLog.Info("user-config-discard", "90-user.json", "User config draft discarded (MCP)")
	return nil
}

// userConfigRevision names the slot's content as it stands, for
// save_singbox_user_config's expectRevision: a truncated SHA-256 of the
// bytes GetSingboxUserConfig reads, taken before any truncation.
func userConfigRevision(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}
