package subscription

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

type recordingSubscriptionDownloader struct {
	calls       []Request
	body        string
	contentType string
	err         error
}

func (d *recordingSubscriptionDownloader) ReadAll(_ context.Context, req Request) ([]byte, string, error) {
	d.calls = append(d.calls, req)
	if d.err != nil {
		return nil, "", d.err
	}
	return []byte(d.body), d.contentType, nil
}

func TestFetchWithContext_InjectedDownloaderPreservesRequest(t *testing.T) {
	svc, _ := newTestService(t)
	dl := &recordingSubscriptionDownloader{body: "ok", contentType: "text/plain"}
	svc.SetDownloader(dl)

	body, contentType, err := svc.fetchWithContext(context.Background(), "https://203.0.113.10/sub", []Header{
		{Name: "User-Agent", Value: "Happ/test"},
		{Name: "X-Custom", Value: "value"},
		{Name: "Host", Value: "forbidden.example"},
	}, FetchOpts{Timeout: 3 * time.Second, MaxBodyBytes: 1234})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" || contentType != "text/plain" {
		t.Fatalf("body=%q contentType=%q", body, contentType)
	}
	if len(dl.calls) != 1 {
		t.Fatalf("calls=%d, want 1", len(dl.calls))
	}
	req := dl.calls[0]
	if req.Method != http.MethodGet || req.URL != "https://203.0.113.10/sub" {
		t.Fatalf("request=%+v", req)
	}
	if req.UserAgent != "Happ/test" || req.Headers.Get("X-Custom") != "value" {
		t.Fatalf("headers=%+v User-Agent=%q", req.Headers, req.UserAgent)
	}
	if req.Headers.Get("Host") != "" || req.Timeout != 3*time.Second || req.MaxBodyBytes != 1234 {
		t.Fatalf("request options=%+v", req)
	}
	if len(req.AllowedStatus) != 200 || req.AllowedStatus[0] != 200 || req.AllowedStatus[199] != 399 {
		t.Fatalf("AllowedStatus length=%d", len(req.AllowedStatus))
	}
}

func TestService_URLReadPaths_UseInjectedDownloader(t *testing.T) {
	svc, _ := newTestService(t)
	dl := &recordingSubscriptionDownloader{body: twoLinks, contentType: "text/plain"}
	svc.SetDownloader(dl)
	const source = "https://203.0.113.10/sub"

	sub, err := svc.Create(context.Background(), CreateInput{Label: "url", URL: source, Enabled: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(dl.calls) != 1 {
		t.Fatalf("Create calls=%d, want 1", len(dl.calls))
	}
	if _, err := svc.Refresh(context.Background(), sub.ID); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, err := svc.PreviewURL(context.Background(), source, nil); err != nil {
		t.Fatalf("PreviewURL: %v", err)
	}
	profile, err := svc.DetectHeaders(context.Background(), source, nil)
	if err != nil {
		t.Fatalf("DetectHeaders: %v", err)
	}
	if profile.ServerCount != 2 {
		t.Fatalf("ServerCount=%d, want 2", profile.ServerCount)
	}
	if len(dl.calls) != 4 {
		t.Fatalf("calls=%d, want create+refresh+preview+detect = 4", len(dl.calls))
	}
	for i, req := range dl.calls {
		if req.URL != source {
			t.Fatalf("call %d URL=%q, want %q", i, req.URL, source)
		}
	}
}

func TestService_Refresh_DownloadErrorKeepsMembersAndDoesNotReload(t *testing.T) {
	svc, mutator := newTestService(t)
	dl := &recordingSubscriptionDownloader{body: twoLinks, contentType: "text/plain"}
	svc.SetDownloader(dl)

	sub, err := svc.Create(context.Background(), CreateInput{
		Label: "url", URL: "https://203.0.113.10/sub", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before, err := svc.store.Get(sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	reloads := mutator.reloads
	calls := len(dl.calls)
	dl.err = errors.New("selected route unavailable")

	if _, err := svc.Refresh(context.Background(), sub.ID); err == nil || !strings.Contains(err.Error(), "selected route unavailable") {
		t.Fatalf("Refresh error=%v", err)
	}
	if len(dl.calls) != calls+1 {
		t.Fatalf("calls=%d, want exactly one failed attempt", len(dl.calls))
	}
	after, err := svc.store.Get(sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(after.MemberTags, ",") != strings.Join(before.MemberTags, ",") {
		t.Fatalf("members changed: before=%v after=%v", before.MemberTags, after.MemberTags)
	}
	if mutator.reloads != reloads {
		t.Fatalf("reloads=%d, want unchanged %d", mutator.reloads, reloads)
	}
}

func TestService_NonURLSources_DoNotUseDownloader(t *testing.T) {
	t.Run("inline", func(t *testing.T) {
		svc, _ := newTestService(t)
		dl := &recordingSubscriptionDownloader{err: errors.New("must not be called")}
		svc.SetDownloader(dl)
		sub, err := svc.Create(context.Background(), CreateInput{Label: "inline", Inline: twoLinks, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Refresh(context.Background(), sub.ID); err != nil {
			t.Fatal(err)
		}
		if len(dl.calls) != 0 {
			t.Fatalf("calls=%d, want 0", len(dl.calls))
		}
	})

	t.Run("file", func(t *testing.T) {
		svc, _ := newTestService(t)
		dl := &recordingSubscriptionDownloader{err: errors.New("must not be called")}
		svc.SetDownloader(dl)
		dir := fileSandbox(t, svc)
		path := writeFileSource(t, dir, "sub.txt")
		sub, err := svc.Create(context.Background(), CreateInput{Label: "file", Path: path, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Refresh(context.Background(), sub.ID); err != nil {
			t.Fatal(err)
		}
		if len(dl.calls) != 0 {
			t.Fatalf("calls=%d, want 0", len(dl.calls))
		}
	})
}
