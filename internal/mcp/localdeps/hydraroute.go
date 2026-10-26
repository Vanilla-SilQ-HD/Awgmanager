package localdeps

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hoaxisr/awg-manager/internal/downloader"
	"github.com/hoaxisr/awg-manager/internal/events"
	"github.com/hoaxisr/awg-manager/internal/hydraroute"
	"github.com/hoaxisr/awg-manager/internal/logging"
	mcpsrv "github.com/hoaxisr/awg-manager/internal/mcp"
)

type (
	// HydraRoute is the part of *hydraroute.Service the HydraRoute tools
	// use.
	HydraRoute interface {
		RefreshStatus() hydraroute.Status
		Control(action string) error
		ReadConfig() (*hydraroute.Config, error)
		SyncGeoFilesToConfig() error
		OversizedTags(ctx context.Context) ([]hydraroute.OversizedTag, error)
		RemoveOversizedTag(name string) error
		// UpdateConfig reads, edits and writes hrneo.conf in one locked
		// step (hydraroute.Service.UpdateConfig).
		UpdateConfig(edit func(*hydraroute.Config) error) (*hydraroute.Config, error)
		CalculateIpsetUsage() (*hydraroute.IpsetUsage, error)
	}
	// HydraGeo is the part of *hydraroute.GeoDataStore the geo tools use.
	HydraGeo interface {
		List() []hydraroute.GeoFileEntry
		GetTags(path string) ([]hydraroute.GeoTag, error)
		UpdateWithClientVia(ctx context.Context, path string, client *http.Client, routeLabel string) (*hydraroute.GeoFileEntry, error)
		UpdateAllWithClientVia(ctx context.Context, client *http.Client, routeLabel string) (int, error)
	}
	// Downloads picks the HTTP client service downloads go through.
	Downloads interface {
		ResolveClient(ctx context.Context, route *downloader.Route) (*downloader.Lease, error)
	}
)

func hydraSetting(c *hydraroute.Config) mcpsrv.HydraRouteSetting {
	return mcpsrv.HydraRouteSetting{AutoStart: c.AutoStart, ClearIPSet: c.ClearIPSet, IpsetTimeoutEnabled: c.IpsetEnableTimeout, IpsetTimeout: c.IpsetTimeout,
		IpsetMaxElem: c.IpsetMaxElem, ConntrackFlush: c.ConntrackFlush, DirectRoute: c.DirectRouteEnabled, GlobalRouting: c.GlobalRouting}
}

func (l *Local) HydraRouteStatus(ctx context.Context) (mcpsrv.HydraRouteStatus, error) {
	if l.c.Hydra == nil {
		return mcpsrv.HydraRouteStatus{State: "not_installed"}, nil
	}
	st := l.c.Hydra.RefreshStatus()
	out := mcpsrv.HydraRouteStatus{Installed: st.Installed, Running: st.Running, State: token(string(st.ProcessState)), Version: sanitizeLabel(st.Version)}
	if !st.Installed {
		return out, nil
	}
	if cfg, err := l.c.Hydra.ReadConfig(); err != nil || cfg == nil {
		out.SettingsError = "hrneo.conf could not be read; set_hydraroute_settings refuses until it can be"
	} else {
		out.Settings = hydraSetting(cfg)
	}
	if u, err := l.c.Hydra.CalculateIpsetUsage(); err != nil {
		out.IpsetUsageError = sanitizeText(err.Error(), maxMessageRunes)
	} else if u != nil {
		out.IpsetMaxElem, out.IpsetUsage = u.MaxElem, u.Usage
	}
	if tags, err := l.c.Hydra.OversizedTags(ctx); err != nil {
		out.DisabledTagsError = sanitizeText(err.Error(), maxMessageRunes)
	} else {
		for _, t := range tags {
			out.DisabledTags = append(out.DisabledTags, mcpsrv.HydraRouteTag{Name: sanitizeLabel(t.Name), Count: t.Count})
		}
	}
	return out, nil
}

func (l *Local) ControlHydraRoute(ctx context.Context, action string) (mcpsrv.HydraRouteStatus, error) {
	if l.c.Hydra == nil {
		return mcpsrv.HydraRouteStatus{}, errUnavailable("HydraRoute Neo")
	}
	if err := l.c.Hydra.Control(action); err != nil {
		l.dnsLog.Warn("hydraroute-control", action, "HydraRoute "+action+" failed (MCP): "+err.Error())
		return mcpsrv.HydraRouteStatus{}, err
	}
	l.dnsLog.Info("hydraroute-control", action, "HydraRoute "+action+" (MCP)")
	l.publish(events.ResourceRoutingHydrarouteStatus, "mcp-control")
	return l.HydraRouteStatus(ctx)
}

// UpdateHydraRouteSettings lays the patch over hrneo.conf: the file is
// written whole, as the REST editor writes it, but read and written in one
// step under the service's lock (UpdateConfig). The service schedules the
// daemon's restart itself; the status event mirrors the REST UpdateConfig.
func (l *Local) UpdateHydraRouteSettings(ctx context.Context, p mcpsrv.HydraRouteSettingsPatch) (mcpsrv.HydraRouteStatus, error) {
	if l.c.Hydra == nil {
		return mcpsrv.HydraRouteStatus{}, errUnavailable("HydraRoute Neo")
	}
	// The fields are laid over the file as it is under the write's lock: a
	// policy order or geo file list written meanwhile stays.
	_, err := l.c.Hydra.UpdateConfig(func(cfg *hydraroute.Config) error {
		for dst, src := range map[*bool]*bool{&cfg.AutoStart: p.AutoStart, &cfg.ClearIPSet: p.ClearIPSet, &cfg.IpsetEnableTimeout: p.IpsetTimeoutEnabled, &cfg.ConntrackFlush: p.ConntrackFlush} {
			if src != nil {
				*dst = *src
			}
		}
		if p.IpsetTimeout != nil {
			cfg.IpsetTimeout = *p.IpsetTimeout
		}
		if p.IpsetMaxElem != nil {
			cfg.IpsetMaxElem = *p.IpsetMaxElem
		}
		return nil
	})
	if errors.Is(err, hydraroute.ErrNoConfig) {
		return mcpsrv.HydraRouteStatus{}, fmt.Errorf("HydraRoute Neo has no hrneo.conf on this router — it is not installed, or has not started yet; nothing was written (get_hydraroute_status shows whether it is installed)")
	}
	if err != nil {
		l.dnsLog.Warn("hydraroute-config", "", "Failed to write HydraRoute settings (MCP): "+err.Error())
		return mcpsrv.HydraRouteStatus{}, err
	}
	l.dnsLog.Info("hydraroute-config", "", "HydraRoute settings written (MCP)")
	l.publish(events.ResourceRoutingHydrarouteStatus, "mcp-config")
	return l.HydraRouteStatus(ctx)
}

func (l *Local) ListHydraRouteGeoFiles(context.Context) ([]mcpsrv.HydraRouteGeoFile, error) {
	if l.c.HydraGeo == nil {
		return nil, errUnavailable("HydraRoute geo files")
	}
	var out []mcpsrv.HydraRouteGeoFile
	for _, f := range l.c.HydraGeo.List() {
		g := mcpsrv.HydraRouteGeoFile{Type: token(f.Type), Path: f.Path, Host: hostOf(f.URL), Size: f.Size, TagCount: f.TagCount, External: f.External}
		if _, err := time.Parse(time.RFC3339, f.Updated); err == nil {
			g.Updated = f.Updated
		}
		out = append(out, g)
	}
	return out, nil
}

func (l *Local) HydraRouteGeoTags(_ context.Context, path string, offset int) (mcpsrv.HydraRouteGeoTags, error) {
	if l.c.HydraGeo == nil {
		return mcpsrv.HydraRouteGeoTags{}, errUnavailable("HydraRoute geo files")
	}
	known := false
	for _, f := range l.c.HydraGeo.List() {
		if f.Path == path {
			known = true
		}
	}
	if !known {
		return mcpsrv.HydraRouteGeoTags{}, fmt.Errorf("%q is not a geo file (use list_hydraroute_geo_files)", path)
	}
	tags, err := l.c.HydraGeo.GetTags(path)
	if err != nil {
		return mcpsrv.HydraRouteGeoTags{}, err
	}
	out := mcpsrv.HydraRouteGeoTags{Path: path, Total: len(tags)}
	if offset > len(tags) {
		offset = len(tags)
	}
	end := min(offset+mcpsrv.MaxGeoTagsInOutput, len(tags))
	for _, t := range tags[offset:end] {
		out.Tags = append(out.Tags, mcpsrv.HydraRouteGeoTag{Name: sanitizeLabel(t.Name), Count: t.Count})
	}
	out.Truncated = end < len(tags)
	return out, nil
}

// UpdateHydraRouteGeoFiles mirrors api.HydraRouteHandler.UpdateGeoFile:
// the download goes through the route service downloads use, then the geo
// keys of hrneo.conf are synced, which restarts the daemon.
func (l *Local) UpdateHydraRouteGeoFiles(ctx context.Context, path string) (mcpsrv.HydraRouteGeoUpdate, error) {
	if l.c.Hydra == nil || l.c.HydraGeo == nil || l.c.Downloads == nil {
		return mcpsrv.HydraRouteGeoUpdate{}, errUnavailable("HydraRoute geo files")
	}
	lease, err := l.c.Downloads.ResolveClient(ctx, nil)
	if err != nil {
		return mcpsrv.HydraRouteGeoUpdate{}, fmt.Errorf("no route for the download: %w", err)
	}
	defer lease.Close()
	label := lease.Route.DisplayName()
	var out mcpsrv.HydraRouteGeoUpdate
	if path == "" {
		n, err := l.c.HydraGeo.UpdateAllWithClientVia(ctx, lease.Client, label)
		out.Updated = n
		if err != nil {
			if n == 0 {
				l.dnsLog.Warn("hydraroute-geo", "", "Geo update failed (MCP): "+err.Error())
				return mcpsrv.HydraRouteGeoUpdate{}, fmt.Errorf("the geo files could not be updated via %s: %s", label, geoError(err))
			}
			out.DownloadError = geoError(err)
		}
	} else {
		if _, err := l.c.HydraGeo.UpdateWithClientVia(ctx, path, lease.Client, label); err != nil {
			return mcpsrv.HydraRouteGeoUpdate{}, fmt.Errorf("the geo file could not be updated via %s: %s", label, geoError(err))
		}
		out.Updated = 1
	}
	if err := l.c.Hydra.SyncGeoFilesToConfig(); err != nil {
		out.SyncError = sanitizeText(err.Error(), maxMessageRunes)
		l.dnsLog.Warn("hydraroute-geo", "", "Geo files updated but not synced to hrneo.conf (MCP): "+err.Error())
	} else {
		out.ConfigSynced = true
	}
	l.dnsLog.Info("hydraroute-geo", path, fmt.Sprintf("%d geo files updated (MCP)", out.Updated))
	l.publish(events.ResourceRoutingHydrarouteStatus, "mcp-geo-update")
	return out, nil
}

// geoError is the store's error for the agent: it quotes the source URL
// whole («re-download <url>: …»), and a custom source may carry a token
// or a login — list_hydraroute_geo_files names the host only.
func geoError(err error) string {
	return sanitizeText(logging.RedactURLs(err.Error()), maxMessageRunes)
}

func (l *Local) RemoveHydraRouteDisabledTag(_ context.Context, name string) error {
	if l.c.Hydra == nil {
		return errUnavailable("HydraRoute Neo")
	}
	err := l.c.Hydra.RemoveOversizedTag(name)
	switch {
	case errors.Is(err, hydraroute.ErrOversizedTagNotFound):
		return fmt.Errorf("%q is not a disabled tag (get_hydraroute_status lists them)", name)
	case err != nil:
		return err
	}
	l.dnsLog.Info("hydraroute-oversized", name, "Disabled tag removed (MCP)")
	l.publish(events.ResourceRoutingDnsRoutes, "mcp-hr-oversized")
	return nil
}
