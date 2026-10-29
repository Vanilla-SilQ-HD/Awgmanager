package router

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hoaxisr/awg-manager/internal/storage"
)

func TestUpdateSettingsWith_PreservesInterleavedWrites(t *testing.T) {
	for _, duringValidation := range []bool{false, true} {
		name := "web-save-before-validation"
		if duringValidation {
			name = "narrow-write-before-commit"
		}
		t.Run(name, func(t *testing.T) {
			h := newTransitionHarness(t)
			h.seedState(t, stateTProxy, false)
			if err := h.store.Update(func(cur *storage.Settings) error {
				cur.SingboxRouter.CacheFileLocation = "flash"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			changed := false
			interleave := func() {
				if changed {
					return
				}
				changed = true
				if duringValidation {
					// Narrow writers (e.g. captureFakeIPRealServerEdit) use the
					// store lock without transitionMu. Holding only that mutex
					// around the MCP read would still lose their changes.
					err := h.store.Update(func(cur *storage.Settings) error {
						cur.SingboxRouter.FakeIPRealServer = "9.9.9.9"
						cur.SingboxRouter.QoSClasses = []storage.SingboxQoSClass{{DSCP: 8, Name: "web", Outbound: "direct"}}
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
				} else {
					web, err := h.svc.GetSettings(ctx)
					if err != nil {
						t.Fatal(err)
					}
					web.UDPTimeout = "10m"
					web.CacheFileLocation = "tmp"
					if err := h.svc.UpdateSettings(ctx, web); err != nil {
						t.Fatal(err)
					}
				}
			}
			var applied []string
			h.svc.deps.ApplyCacheFileLocation = func(loc string) error {
				applied = append(applied, loc)
				if duringValidation {
					interleave()
				}
				return nil
			}
			edits := 0
			err := h.svc.UpdateSettingsWith(ctx, func(sr *storage.SingboxRouterSettings) error {
				edits++
				if !duringValidation {
					interleave()
				}
				sr.SnifferEnabled = true
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			saved, err := h.svc.GetSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if edits != 2 || !saved.SnifferEnabled {
				t.Fatalf("patch must be rebuilt after the conflict: edits=%d settings=%+v", edits, saved)
			}
			if duringValidation {
				if saved.FakeIPRealServer != "9.9.9.9" || len(saved.QoSClasses) != 1 || saved.QoSClasses[0].Name != "web" {
					t.Fatalf("narrow writes lost: %+v", saved)
				}
			} else {
				// Once the web save has applied tmp, the stale MCP attempt
				// must not move the cache back to flash before detecting the
				// conflict. A real move removes the old cache and schedules a
				// reload; restoring the final setting cannot undo that.
				if saved.UDPTimeout != "10m" || saved.CacheFileLocation != "tmp" || !reflect.DeepEqual(applied, []string{"tmp", "tmp"}) {
					t.Fatalf("only the web save and rebuilt patch may apply tmp: %+v, applied=%v", saved, applied)
				}
			}
		})
	}
}

func TestUpdateSettingsWith_RefusalsSaveNothing(t *testing.T) {
	for _, kind := range []string{"edit", "validation", "transition", "canceled", "conflicts"} {
		t.Run(kind, func(t *testing.T) {
			h := newTransitionHarness(t)
			h.seedState(t, stateTProxy, false)
			before, err := h.svc.GetSettings(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var want error
			sentinel := errors.New("edit refused")
			if kind == "canceled" {
				cancel()
				want = context.Canceled
			}
			if kind == "transition" {
				h.svc.transitionMu.Lock()
				defer h.svc.transitionMu.Unlock()
				want = ErrTransitionInProgress
			}
			if kind == "edit" {
				want = sentinel
			}
			if kind == "conflicts" {
				want = errSettingsChanged
				h.svc.deps.ApplyCacheFileLocation = func(string) error {
					return h.store.Update(func(cur *storage.Settings) error {
						if cur.SingboxRouter.UDPTimeout == "10m" {
							cur.SingboxRouter.UDPTimeout = "5m"
						} else {
							cur.SingboxRouter.UDPTimeout = "10m"
						}
						return nil
					})
				}
			}
			edits := 0
			err = h.svc.UpdateSettingsWith(ctx, func(sr *storage.SingboxRouterSettings) error {
				edits++
				sr.SnifferEnabled = true
				if kind == "validation" {
					sr.UDPTimeout = "invalid"
				}
				if kind == "edit" {
					return sentinel
				}
				return nil
			})
			if err == nil || (want != nil && !errors.Is(err, want)) {
				t.Fatalf("refusal = %v, want %v", err, want)
			}
			if kind == "conflicts" && edits != 3 {
				t.Fatalf("conflict retries must be bounded: %d", edits)
			}
			if kind == "canceled" && edits != 0 {
				t.Fatal("canceled request ran its edit")
			}
			saved, err := h.svc.GetSettings(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if kind == "conflicts" {
				// Only the competing writer's field may differ.
				saved.UDPTimeout = before.UDPTimeout
			}
			if !reflect.DeepEqual(saved, before) {
				t.Fatalf("refused patch changed settings: before=%+v after=%+v", before, saved)
			}
		})
	}
}
