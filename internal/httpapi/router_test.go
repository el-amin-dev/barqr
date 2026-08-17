package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/el-amin-dev/barqr/internal/config"
	"github.com/el-amin-dev/barqr/internal/httpapi"
	"github.com/el-amin-dev/barqr/internal/version"
)

// newServer builds a Server on default configuration with logging discarded.
func newServer(t *testing.T) *httpapi.Server {
	t.Helper()

	cfg, _, err := config.Load(nil)
	if err != nil {
		t.Fatalf("config.Load() returned error: %v", err)
	}
	return httpapi.New(cfg, slog.New(slog.DiscardHandler))
}

// get performs an in-process GET against the server's handler.
func get(t *testing.T, h http.Handler, path string) *http.Response {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}

func TestHealthz(t *testing.T) {
	t.Parallel()

	resp := get(t, newServer(t).Handler(), "/v1/healthz")
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := resp.Header.Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got, want := body["status"], "ok"; got != want {
		t.Errorf("status = %q, want %q", got, want)
	}
}

// TestReadyzBeforeServe asserts the readiness gate is closed until Serve runs,
// so a replica never advertises itself before it can accept connections.
func TestReadyzBeforeServe(t *testing.T) {
	t.Parallel()

	resp := get(t, newServer(t).Handler(), "/v1/readyz")
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusServiceUnavailable; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()

	resp := get(t, newServer(t).Handler(), "/v1/version")
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	var info version.Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got, want := info.Name, version.Name; got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
	if info.Version == "" || info.Go == "" || info.Platform == "" {
		t.Errorf("version info is incomplete: %+v", info)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	t.Parallel()

	resp := get(t, newServer(t).Handler(), "/v1/nope")
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusNotFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

// TestServeLifecycle exercises the readiness flip and graceful shutdown over a
// real socket: ready while serving, draining once the context is cancelled.
func TestServeLifecycle(t *testing.T) {
	t.Parallel()

	srv := newServer(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	base := "http://" + ln.Addr().String()
	waitReady(t, base+"/v1/readyz")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve() did not return after context cancellation")
	}

	// The listener must be closed once Serve returns.
	if _, err := http.Get(base + "/v1/healthz"); err == nil { //nolint:bodyclose // no body on a failed dial
		t.Error("server still accepting connections after shutdown")
	}
}

// waitReady polls until the endpoint reports ready or the deadline passes.
func waitReady(t *testing.T, url string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:gosec,noctx // fixed loopback URL in a test
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never became ready", url)
}
