package httpapi_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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
