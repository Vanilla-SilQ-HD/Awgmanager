package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoaxisr/awg-manager/internal/downloader"
	"github.com/hoaxisr/awg-manager/internal/singbox/subscription"
	"github.com/hoaxisr/awg-manager/internal/sys/httpclient"
)

type subscriptionTestOutbounds struct {
	items []downloader.Outbound
}

func (p subscriptionTestOutbounds) ListDownloadOutbounds(context.Context) []downloader.Outbound {
	return p.items
}

type subscriptionTestRoute struct {
	route downloader.Route
}

func (p subscriptionTestRoute) GetDownloadRoute(context.Context) (*downloader.Route, error) {
	return &p.route, nil
}

type subscriptionTestTransport struct {
	spec downloader.TransportSpec
}

func (r subscriptionTestTransport) Resolve(context.Context, downloader.Outbound) (downloader.TransportSpec, error) {
	return r.spec, nil
}

func (r subscriptionTestTransport) IsAvailable(context.Context, downloader.Outbound) bool {
	return true
}

func TestSingboxSubscriptionDownloaderAdapter_DirectRequest(t *testing.T) {
	restoreGuard := httpclient.AllowInternalDialForTest()
	defer restoreGuard()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")

	var gotUA, gotCustom string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotCustom = r.Header.Get("X-Custom")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("direct-body"))
	}))
	defer origin.Close()

	adapter := &singboxSubscriptionDownloaderAdapter{svc: downloader.NewService(downloader.Deps{})}
	body, contentType, err := adapter.ReadAll(context.Background(), subscription.Request{
		Method:       http.MethodGet,
		URL:          origin.URL,
		Headers:      http.Header{"X-Custom": []string{"value"}},
		Timeout:      2 * time.Second,
		MaxBodyBytes: 128,
		UserAgent:    "Happ/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "direct-body" || contentType != "text/plain; charset=utf-8" {
		t.Fatalf("body=%q contentType=%q", body, contentType)
	}
	if gotUA != "Happ/test" || gotCustom != "value" {
		t.Fatalf("User-Agent=%q X-Custom=%q", gotUA, gotCustom)
	}
}

func TestSingboxSubscriptionDownloaderAdapter_UsesSelectedProxyRoute(t *testing.T) {
	proxyHits := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits++
		if r.RequestURI != "http://203.0.113.10/sub" {
			t.Errorf("RequestURI=%q, want absolute subscription URL", r.RequestURI)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("proxy-body"))
	}))
	defer proxy.Close()
	_, portText, err := net.SplitHostPort(proxy.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	shared := downloader.NewService(downloader.Deps{
		Outbounds: subscriptionTestOutbounds{items: []downloader.Outbound{
			{Tag: "DE", Kind: "singbox", Label: "Germany"},
		}},
		RouteProvider: subscriptionTestRoute{route: downloader.Route{Tag: "DE", Kind: "singbox"}},
		TransportResolver: subscriptionTestTransport{spec: downloader.TransportSpec{
			Mode: downloader.TransportModeProxy, ProxyPort: port,
		}},
	})
	adapter := &singboxSubscriptionDownloaderAdapter{svc: shared}
	body, _, err := adapter.ReadAll(context.Background(), subscription.Request{
		Method:       http.MethodGet,
		URL:          "http://203.0.113.10/sub",
		Timeout:      2 * time.Second,
		MaxBodyBytes: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "proxy-body" || proxyHits != 1 {
		t.Fatalf("body=%q proxyHits=%d", body, proxyHits)
	}
}

func TestSingboxSubscriptionDownloaderAdapter_RejectsNonHTTPRedirect(t *testing.T) {
	restoreGuard := httpclient.AllowInternalDialForTest()
	defer restoreGuard()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://203.0.113.10/sub", http.StatusFound)
	}))
	defer redirect.Close()

	adapter := &singboxSubscriptionDownloaderAdapter{svc: downloader.NewService(downloader.Deps{})}
	_, _, err := adapter.ReadAll(context.Background(), subscription.Request{
		Method:       http.MethodGet,
		URL:          redirect.URL,
		Timeout:      2 * time.Second,
		MaxBodyBytes: 128,
	})
	if err == nil || !strings.Contains(err.Error(), "http(s)") {
		t.Fatalf("expected non-HTTP redirect rejection, got %v", err)
	}
}
