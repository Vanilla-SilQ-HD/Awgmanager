package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadAll_DirectPublicClientBlocksInternalDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("request reached loopback despite the dial guard")
	}))
	defer srv.Close()

	svc := NewService(Deps{})
	_, _, err := svc.ReadAll(context.Background(), Request{
		URL:                srv.URL,
		Timeout:            time.Second,
		MaxBodyBytes:       128,
		DirectPublicClient: true,
	})
	if err == nil || !strings.Contains(err.Error(), "внутренний адрес") {
		t.Fatalf("expected internal dial rejection, got %v", err)
	}
}
