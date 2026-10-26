package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/hoaxisr/awg-manager/internal/storage"
)

// EditTunnel — та же правка, что POST /tunnels/update, без HTTP: её
// вызывает MCP. Отказ приходит типизированным, со статусом и кодом REST,
// а успешная правка ложится в стор.
func TestEditTunnel_RefusalCarriesStatusAndCode(t *testing.T) {
	h, store := newTunnelsUpdateHarness(t, &stubTunnelSvc{})
	seedTunnel(t, store, &storage.AWGTunnel{
		ID: "awg1", Name: "t1", Enabled: true, Locked: true,
		Interface: storage.AWGInterface{Address: "10.0.0.2/32"},
	})
	seedTunnel(t, store, &storage.AWGTunnel{
		ID: "awg2", Name: "t2", Enabled: true,
		Interface: storage.AWGInterface{Address: "10.0.0.3/32", MTU: 1420},
		Peer:      storage.AWGPeer{Endpoint: "1.2.3.4:51820", PublicKey: "pub"},
	})

	_, err := h.EditTunnel(context.Background(), "awg1", storage.AWGTunnel{Name: "x"})
	var editErr *TunnelEditError
	if !errors.As(err, &editErr) || editErr.Status != http.StatusForbidden || editErr.Code != "TUNNEL_LOCKED" {
		t.Fatalf("err = %#v", err)
	}
	_, err = h.EditTunnel(context.Background(), "absent", storage.AWGTunnel{Name: "x"})
	if !errors.As(err, &editErr) || editErr.Status != http.StatusBadRequest || editErr.Code != "NOT_FOUND" {
		t.Fatalf("err = %#v", err)
	}

	edited, err := h.EditTunnel(context.Background(), "awg2", storage.AWGTunnel{Name: "renamed"})
	if err != nil || edited.Raw != nil {
		t.Fatalf("edited = %+v, err = %v", edited, err)
	}
	if saved, _ := store.Get("awg2"); saved.Name != "renamed" || saved.Interface.MTU != 1420 {
		t.Fatalf("saved = %+v", saved)
	}
}

// EditTunnelWith строит правку по записи, которую читает сама: правка из
// веб-интерфейса, легшая раньше, видна сборщику и не перезаписывается
// целым блоком из устаревшего снимка. Отказ сборщика ничего не пишет, а
// защищённый туннель отвергается до сборки.
func TestEditTunnelWith_BuildsOnTheRecordItEdits(t *testing.T) {
	h, store := newTunnelsUpdateHarness(t, &stubTunnelSvc{})
	seedTunnel(t, store, &storage.AWGTunnel{
		ID: "awg2", Name: "t2", Enabled: true,
		Interface: storage.AWGInterface{Address: "10.0.0.3/32", MTU: 1420, DNS: "1.1.1.1"},
		Peer:      storage.AWGPeer{Endpoint: "1.2.3.4:51820", PublicKey: "pub"},
	})
	// The web interface changes DNS after the caller read the tunnel.
	if _, err := h.EditTunnel(context.Background(), "awg2", storage.AWGTunnel{Interface: storage.AWGInterface{Address: "10.0.0.3/32", MTU: 1420, DNS: "9.9.9.9"}}); err != nil {
		t.Fatal(err)
	}
	_, err := h.EditTunnelWith(context.Background(), "awg2", func(existing storage.AWGTunnel) (storage.AWGTunnel, error) {
		iface := existing.Interface
		iface.MTU = 1380
		return storage.AWGTunnel{Interface: iface}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved, _ := store.Get("awg2"); saved.Interface.MTU != 1380 || saved.Interface.DNS != "9.9.9.9" {
		t.Fatalf("the block must be built from the record the edit reads: %+v", saved.Interface)
	}

	refused := errors.New("refused")
	if _, err := h.EditTunnelWith(context.Background(), "awg2", func(storage.AWGTunnel) (storage.AWGTunnel, error) {
		return storage.AWGTunnel{Name: "never"}, refused
	}); err != refused {
		t.Fatalf("the builder's refusal must come back as is: %v", err)
	}
	if saved, _ := store.Get("awg2"); saved.Name != "t2" {
		t.Fatalf("a refused build must write nothing: %+v", saved)
	}

	seedTunnel(t, store, &storage.AWGTunnel{ID: "awg1", Name: "t1", Enabled: true, Locked: true, Interface: storage.AWGInterface{Address: "10.0.0.2/32"}})
	built := false
	_, err = h.EditTunnelWith(context.Background(), "awg1", func(e storage.AWGTunnel) (storage.AWGTunnel, error) { built = true; return e, nil })
	var editErr *TunnelEditError
	if !errors.As(err, &editErr) || editErr.Code != "TUNNEL_LOCKED" || built {
		t.Fatalf("a locked tunnel is refused before the build: %v, built = %v", err, built)
	}
}
