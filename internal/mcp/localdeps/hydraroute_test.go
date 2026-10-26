package localdeps

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hoaxisr/awg-manager/internal/downloader"
	"github.com/hoaxisr/awg-manager/internal/events"
	"github.com/hoaxisr/awg-manager/internal/hydraroute"
	mcpsrv "github.com/hoaxisr/awg-manager/internal/mcp"
)

type fakeHydra struct {
	st         hydraroute.Status
	cfg        hydraroute.Config
	written    *hydraroute.Config
	controlled string
	synced     int
	syncErr    error
	removeErr  error
	removed    string
	readErr    error
	beforeEdit func(*fakeHydra)
	// noConfig is a router without hrneo.conf; tagsErr and usageErr fail
	// those reads.
	noConfig bool
	tagsErr  error
	usageErr error
}

func (f *fakeHydra) RefreshStatus() hydraroute.Status { return f.st }
func (f *fakeHydra) Control(action string) error {
	f.controlled = action
	return nil
}
func (f *fakeHydra) ReadConfig() (*hydraroute.Config, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	c := f.cfg
	return &c, nil
}
func (f *fakeHydra) WriteConfig(cfg *hydraroute.Config) error {
	f.written = cfg
	f.cfg = *cfg
	return nil
}

// UpdateConfig mirrors hydraroute.Service.UpdateConfig: beforeEdit is a
// write that lands between another caller's read and this write — it must
// be what edit sees.
func (f *fakeHydra) UpdateConfig(edit func(*hydraroute.Config) error) (*hydraroute.Config, error) {
	if f.noConfig {
		return nil, hydraroute.ErrNoConfig
	}
	if f.beforeEdit != nil {
		f.beforeEdit(f)
	}
	cfg, err := f.ReadConfig()
	if err != nil {
		return nil, err
	}
	if err := edit(cfg); err != nil {
		return nil, err
	}
	return cfg, f.WriteConfig(cfg)
}
func (f *fakeHydra) SyncGeoFilesToConfig() error {
	f.synced++
	return f.syncErr
}
func (f *fakeHydra) OversizedTags(context.Context) ([]hydraroute.OversizedTag, error) {
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	return []hydraroute.OversizedTag{{Name: "geoip:ru", Count: 90000, File: "/opt/geo/geoip.dat"}}, nil
}
func (f *fakeHydra) RemoveOversizedTag(name string) error {
	f.removed = name
	return f.removeErr
}
func (f *fakeHydra) CalculateIpsetUsage() (*hydraroute.IpsetUsage, error) {
	if f.usageErr != nil {
		return nil, f.usageErr
	}
	return &hydraroute.IpsetUsage{MaxElem: 65536, Usage: map[string]int{"nwg0": 12}}, nil
}

type fakeGeo struct {
	files   []hydraroute.GeoFileEntry
	tags    []hydraroute.GeoTag
	allN    int
	allErr  error
	oneErr  error
	via     string
	updated string
}

func (f *fakeGeo) List() []hydraroute.GeoFileEntry { return f.files }
func (f *fakeGeo) GetTags(string) ([]hydraroute.GeoTag, error) {
	return f.tags, nil
}
func (f *fakeGeo) UpdateWithClientVia(_ context.Context, path string, _ *http.Client, label string) (*hydraroute.GeoFileEntry, error) {
	f.updated, f.via = path, label
	return &hydraroute.GeoFileEntry{Path: path}, f.oneErr
}
func (f *fakeGeo) UpdateAllWithClientVia(_ context.Context, _ *http.Client, label string) (int, error) {
	f.updated, f.via = "*", label
	return f.allN, f.allErr
}

type fakeDownloads struct{ err error }

func (f fakeDownloads) ResolveClient(context.Context, *downloader.Route) (*downloader.Lease, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &downloader.Lease{Client: http.DefaultClient, Route: downloader.RouteInfo{Tag: "proxy-1", Kind: "vless"}}, nil
}

// TestLocal_HydraRouteStatusAndSettings — статус собирает демона, ipset и
// отключённые теги; настройки пишутся поверх прочитанного hrneo.conf, а
// global routing и geo-списки, которых нет в патче, остаются как были.
func TestLocal_HydraRouteStatusAndSettings(t *testing.T) {
	if st, err := New(Config{}).HydraRouteStatus(context.Background()); err != nil || st.State != "not_installed" {
		t.Fatalf("no HR on the build: %+v, %v", st, err)
	}
	h := &fakeHydra{st: hydraroute.Status{Installed: true, Running: true, ProcessState: hydraroute.StateRunning, Version: "2.3.1"},
		cfg: hydraroute.Config{AutoStart: true, GlobalRouting: true, IpsetMaxElem: 65536, IpsetTimeout: 21600, GeoIPFiles: []string{"/opt/geo/geoip.dat"}, PolicyOrder: []string{"HydraRoute"}}}
	bus := &recBus{}
	l := New(Config{Hydra: h, Bus: bus})
	st, err := l.HydraRouteStatus(context.Background())
	if err != nil || st.State != "running" || st.Version != "2.3.1" || st.IpsetUsage["nwg0"] != 12 || len(st.DisabledTags) != 1 || st.DisabledTags[0].Count != 90000 || !st.Settings.GlobalRouting || st.SettingsError != "" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	h.readErr = errors.New("hrneo.conf: permission denied")
	if st, err = l.HydraRouteStatus(context.Background()); err != nil || st.SettingsError == "" || st.State != "running" {
		t.Fatalf("an unread config is said so, the rest still comes: %+v, %v", st, err)
	}
	h.readErr = nil
	maxElem, off := 262144, false
	st, err = l.UpdateHydraRouteSettings(context.Background(), mcpsrv.HydraRouteSettingsPatch{IpsetMaxElem: &maxElem, AutoStart: &off})
	w := h.written
	if err != nil || w == nil || w.IpsetMaxElem != 262144 || w.AutoStart || !w.GlobalRouting || w.IpsetTimeout != 21600 || len(w.GeoIPFiles) != 1 || len(w.PolicyOrder) != 1 {
		t.Fatalf("written = %+v, %v", w, err)
	}
	if st.Settings.IpsetMaxElem != 262144 || !bus.has(events.ResourceRoutingHydrarouteStatus) {
		t.Fatalf("status after write = %+v, events %v", st.Settings, bus.pub)
	}
	// The policy order is dragged in the web interface between this write's
	// read and the file being written: the patch lands on the file as it is.
	h.beforeEdit = func(f *fakeHydra) { f.cfg.PolicyOrder = []string{"Policy1", "HydraRoute"} }
	if _, err := l.UpdateHydraRouteSettings(context.Background(), mcpsrv.HydraRouteSettingsPatch{AutoStart: &off}); err != nil || len(h.written.PolicyOrder) != 2 || h.written.PolicyOrder[0] != "Policy1" {
		t.Fatalf("a policy order written meanwhile must stay: %+v, %v", h.written, err)
	}
	h.beforeEdit, bus.pub = nil, nil
	if _, err := l.ControlHydraRoute(context.Background(), "restart"); err != nil || h.controlled != "restart" || !bus.has(events.ResourceRoutingHydrarouteStatus) {
		t.Fatalf("control: %v, %q, events %v", err, h.controlled, bus.pub)
	}
}

// TestLocal_HydraRouteGeo — список отдаёт только хост URL; теги
// страничные; обновление идёт через маршрут загрузок и синхронизирует
// hrneo.conf, частичный успех — не ошибка.
func TestLocal_HydraRouteGeo(t *testing.T) {
	h := &fakeHydra{st: hydraroute.Status{Installed: true}}
	g := &fakeGeo{files: []hydraroute.GeoFileEntry{{Type: "geoip", Path: "/opt/geo/geoip.dat", URL: "https://user:secret@github.com/x/geoip.dat?token=abc", Updated: "2026-09-01T00:00:00Z", TagCount: 250}}}
	for i := range 250 {
		g.tags = append(g.tags, hydraroute.GeoTag{Name: "t" + strings.Repeat("x", i%3), Count: i})
	}
	bus := &recBus{}
	l := New(Config{Hydra: h, HydraGeo: g, Downloads: fakeDownloads{}, Bus: bus})
	files, err := l.ListHydraRouteGeoFiles(context.Background())
	if err != nil || len(files) != 1 || files[0].Host != "github.com" || files[0].Updated == "" {
		t.Fatalf("files = %+v, %v", files, err)
	}
	page, err := l.HydraRouteGeoTags(context.Background(), "/opt/geo/geoip.dat", 0)
	if err != nil || len(page.Tags) != mcpsrv.MaxGeoTagsInOutput || !page.Truncated || page.Total != 250 {
		t.Fatalf("page = %d tags, %v, %v", len(page.Tags), page.Truncated, err)
	}
	if page, _ = l.HydraRouteGeoTags(context.Background(), "/opt/geo/geoip.dat", 200); len(page.Tags) != 50 || page.Truncated {
		t.Fatalf("second page = %d, %v", len(page.Tags), page.Truncated)
	}
	if _, err := l.HydraRouteGeoTags(context.Background(), "/etc/passwd", 0); err == nil {
		t.Fatal("a path that is not a geo file must be refused")
	}

	if r, err := l.UpdateHydraRouteGeoFiles(context.Background(), "/opt/geo/geoip.dat"); err != nil || r.Updated != 1 || r.DownloadError != "" || !r.ConfigSynced || g.updated != "/opt/geo/geoip.dat" || g.via != "proxy-1 (vless)" || h.synced != 1 {
		t.Fatalf("one: %+v %v via %q synced %d", r, err, g.via, h.synced)
	}
	if !bus.has(events.ResourceRoutingHydrarouteStatus) {
		t.Fatalf("an update must tell open pages, as REST does: %v", bus.pub)
	}
	g.allN, g.allErr = 2, errors.New("update errors: /opt/geo/geosite.dat: re-download https://user:secret@github.com/x/geosite.dat?token=abc: timeout")
	r, err := l.UpdateHydraRouteGeoFiles(context.Background(), "")
	if err != nil || r.Updated != 2 || !r.ConfigSynced || g.updated != "*" || !strings.Contains(r.DownloadError, "github.com") || strings.Contains(r.DownloadError, "secret") {
		t.Fatalf("some files failing: the reason comes, its address cut to the host, and the sync is told apart: %+v, %v", r, err)
	}
	g.allN = 0
	if _, err := l.UpdateHydraRouteGeoFiles(context.Background(), ""); err == nil {
		t.Fatal("nothing updated must be an error")
	}
	g.allErr = errors.New("update errors: /opt/geo/geoip.dat: re-download https://user:secret@github.com/x/geoip.dat?token=abc: timeout")
	if _, err := l.UpdateHydraRouteGeoFiles(context.Background(), ""); err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token") || !strings.Contains(err.Error(), "github.com") {
		t.Fatalf("the source URL is cut to its host: %v", err)
	}
	g.oneErr = g.allErr
	if _, err := l.UpdateHydraRouteGeoFiles(context.Background(), "/opt/geo/geoip.dat"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("one file: the source URL is cut to its host: %v", err)
	}
	g.oneErr = nil
	g.allErr, g.allN, h.syncErr = nil, 3, errors.New("conf busy")
	if r, err := l.UpdateHydraRouteGeoFiles(context.Background(), ""); err != nil || r.ConfigSynced || r.SyncError != "conf busy" || r.DownloadError != "" || r.Updated != 3 {
		t.Fatalf("a failed sync is told apart from failed downloads, with its reason: %+v, %v", r, err)
	}
	if _, err := New(Config{Hydra: h, HydraGeo: g, Downloads: fakeDownloads{err: errors.New("no route")}}).UpdateHydraRouteGeoFiles(context.Background(), ""); err == nil {
		t.Fatal("no download route must be an error")
	}
}

func TestLocal_RemoveHydraRouteDisabledTag(t *testing.T) {
	h := &fakeHydra{}
	bus := &recBus{}
	l := New(Config{Hydra: h, Bus: bus})
	if err := l.RemoveHydraRouteDisabledTag(context.Background(), "ru"); err != nil || h.removed != "ru" || !bus.has(events.ResourceRoutingDnsRoutes) {
		t.Fatalf("remove: %v, %q, %v", err, h.removed, bus.pub)
	}
	h.removeErr = hydraroute.ErrOversizedTagNotFound
	if err := l.RemoveHydraRouteDisabledTag(context.Background(), "us"); err == nil || !strings.Contains(err.Error(), "get_hydraroute_status") {
		t.Fatalf("unknown tag: %v", err)
	}
}

// TestLocal_HydraRouteReadsThatFailSaySo — несчитанный список отключённых
// тегов — не «отключённых тегов нет», и то же с заполнением ipset; запись
// настроек без hrneo.conf — отказ, а не «сохранено».
func TestLocal_HydraRouteReadsThatFailSaySo(t *testing.T) {
	h := &fakeHydra{st: hydraroute.Status{Installed: true}, cfg: hydraroute.Config{IpsetMaxElem: 65536},
		tagsErr: errors.New("geo index: busy"), usageErr: errors.New("ipset: not found")}
	l := New(Config{Hydra: h})
	st, err := l.HydraRouteStatus(context.Background())
	if err != nil || st.DisabledTags != nil || st.DisabledTagsError == "" || st.IpsetUsage != nil || st.IpsetUsageError == "" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	h.tagsErr, h.usageErr = nil, nil
	if st, _ = l.HydraRouteStatus(context.Background()); st.DisabledTagsError != "" || st.IpsetUsageError != "" || len(st.DisabledTags) != 1 {
		t.Fatalf("reads that work carry no error: %+v", st)
	}

	h.noConfig = true
	off := false
	if _, err := l.UpdateHydraRouteSettings(context.Background(), mcpsrv.HydraRouteSettingsPatch{AutoStart: &off}); err == nil || !strings.Contains(err.Error(), "nothing was written") || h.written != nil {
		t.Fatalf("no hrneo.conf, no write: %v, %+v", err, h.written)
	}
}
