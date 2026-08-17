package config_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/el-amin-dev/barqr/internal/config"
)

// env turns a map into the "KEY=VALUE" slice shape Load expects, so that
// tests never touch the real process environment and can run in parallel.
func env(kv map[string]string) []string {
	out := make([]string, 0, len(kv))
	for k, v := range kv {
		out = append(out, k+"="+v)
	}
	return out
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, warns, err := config.Load(nil)
	if err != nil {
		t.Fatalf("Load(nil) returned error: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("Load(nil) warnings = %v, want none", warns)
	}

	if got, want := cfg.Addr(), "127.0.0.1:3000"; got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}
	if got, want := cfg.AuthMode, config.AuthRequired; got != want {
		t.Errorf("AuthMode = %q, want %q", got, want)
	}
	if got, want := cfg.MaxBody, int64(2<<20); got != want {
		t.Errorf("MaxBody = %d, want %d", got, want)
	}
	if got, want := cfg.RequestTimeout, 10*time.Second; got != want {
		t.Errorf("RequestTimeout = %s, want %s", got, want)
	}
	if got, want := cfg.RateLimit.String(), "120/min"; got != want {
		t.Errorf("RateLimit = %q, want %q", got, want)
	}
	if got, want := cfg.ShutdownGrace, 15*time.Second; got != want {
		t.Errorf("ShutdownGrace = %s, want %s", got, want)
	}
	if cfg.AllowRemoteFetch {
		t.Error("AllowRemoteFetch = true, want false (egress is off by default)")
	}
	if !cfg.Metrics {
		t.Error("Metrics = false, want true")
	}
	if cfg.APIKeyCount() != 0 {
		t.Errorf("APIKeyCount() = %d, want 0", cfg.APIKeyCount())
	}
}

// TestLoadInsecureBindIsFatal covers the two startup invariants from the
// security model: barqr must refuse to boot rather than expose unauthenticated
// or unreachable-by-design code generation to a wider network.
func TestLoadInsecureBindIsFatal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr error
	}{
		{
			name:    "non-loopback bind with auth required and no keys",
			env:     map[string]string{"BARQR_BIND": "0.0.0.0"},
			wantErr: config.ErrInsecure,
		},
		{
			name:    "routable bind with auth required and no keys",
			env:     map[string]string{"BARQR_BIND": "10.1.2.3"},
			wantErr: config.ErrInsecure,
		},
		{
			name: "open auth on wildcard bind without acknowledgement",
			env: map[string]string{
				"BARQR_BIND":      "0.0.0.0",
				"BARQR_AUTH_MODE": "open",
			},
			wantErr: config.ErrInsecure,
		},
		{
			name: "open auth on IPv6 wildcard bind without acknowledgement",
			env: map[string]string{
				"BARQR_BIND":      "::",
				"BARQR_AUTH_MODE": "open",
			},
			wantErr: config.ErrInsecure,
		},
		{
			name: "non-loopback bind with keys is allowed",
			env: map[string]string{
				"BARQR_BIND":     "0.0.0.0",
				"BARQR_API_KEYS": "s3cret",
			},
		},
		{
			name: "open auth on loopback is allowed",
			env: map[string]string{
				"BARQR_BIND":      "127.0.0.1",
				"BARQR_AUTH_MODE": "open",
			},
		},
		{
			name: "open auth on wildcard with explicit acknowledgement is allowed",
			env: map[string]string{
				"BARQR_BIND":                   "0.0.0.0",
				"BARQR_AUTH_MODE":              "open",
				"BARQR_I_UNDERSTAND_OPEN_BIND": "true",
			},
		},
		{
			name: "loopback by hostname needs no keys",
			env: map[string]string{
				"BARQR_BIND": "localhost",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, _, err := config.Load(env(tt.env))
			switch {
			case tt.wantErr != nil && err == nil:
				t.Fatalf("Load() succeeded, want error %v", tt.wantErr)
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Fatalf("Load() error = %v, want %v", err, tt.wantErr)
			case tt.wantErr == nil && err != nil:
				t.Fatalf("Load() returned error: %v", err)
			}
			if tt.wantErr != nil && cfg != nil {
				t.Error("Load() returned a Config alongside a fatal error, want nil")
			}
		})
	}
}

func TestLoadInvalidValuesAreFatal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
	}{
		{"port not a number", map[string]string{"BARQR_PORT": "http"}},
		{"port out of range", map[string]string{"BARQR_PORT": "70000"}},
		{"unknown auth mode", map[string]string{"BARQR_AUTH_MODE": "maybe"}},
		{"bad duration", map[string]string{"BARQR_REQUEST_TIMEOUT": "ten seconds"}},
		{"bad byte size", map[string]string{"BARQR_MAX_BODY": "2 gigs"}},
		{"bad rate", map[string]string{"BARQR_RATE_LIMIT": "120 per minute"}},
		{"bad rate unit", map[string]string{"BARQR_RATE_LIMIT": "120/fortnight"}},
		{"bad boolean", map[string]string{"BARQR_METRICS": "yes-please"}},
		{"unknown output format", map[string]string{"BARQR_DEFAULT_FORMAT": "bmp"}},
		{"unknown ecc level", map[string]string{"BARQR_DEFAULT_ECC": "Z"}},
		{"unknown log level", map[string]string{"BARQR_LOG_LEVEL": "chatty"}},
		{"zero concurrency", map[string]string{"BARQR_CONCURRENCY": "0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, _, err := config.Load(env(tt.env)); !errors.Is(err, config.ErrInvalid) {
				t.Fatalf("Load() error = %v, want %v", err, config.ErrInvalid)
			}
		})
	}
}

// TestLoadReportsEveryProblem asserts that a misconfigured deployment shows
// its whole list of problems in one boot, not one per restart.
func TestLoadReportsEveryProblem(t *testing.T) {
	t.Parallel()

	_, _, err := config.Load(env(map[string]string{
		"BARQR_PORT":      "nope",
		"BARQR_AUTH_MODE": "maybe",
		"BARQR_LOG_LEVEL": "chatty",
	}))
	if err == nil {
		t.Fatal("Load() succeeded, want error")
	}

	for _, key := range []string{"BARQR_PORT", "BARQR_AUTH_MODE", "BARQR_LOG_LEVEL"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not mention %s", err, key)
		}
	}
}

func TestLoadWarnsOnUnknownVariables(t *testing.T) {
	t.Parallel()

	_, warns, err := config.Load(env(map[string]string{
		"BARQR_API_KEY": "typo-singular",
		"PATH":          "/usr/bin",
	}))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warns)
	}
	if !strings.Contains(warns[0], "BARQR_API_KEY") {
		t.Errorf("warning %q does not name the offending variable", warns[0])
	}
}

func TestParsedValues(t *testing.T) {
	t.Parallel()

	cfg, _, err := config.Load(env(map[string]string{
		"BARQR_BIND":            "127.0.0.1",
		"BARQR_PORT":            "8080",
		"BARQR_MAX_BODY":        "512KB",
		"BARQR_FETCH_MAX_BYTES": "1048576",
		"BARQR_RATE_LIMIT":      "10/s",
		"BARQR_CORS_ORIGINS":    "https://a.example, https://b.example ,",
		"BARQR_FETCH_ALLOWLIST": "cdn.example",
	}))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if got, want := cfg.Addr(), "127.0.0.1:8080"; got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}
	if got, want := cfg.MaxBody, int64(512<<10); got != want {
		t.Errorf("MaxBody = %d, want %d", got, want)
	}
	if got, want := cfg.FetchMaxBytes, int64(1<<20); got != want {
		t.Errorf("FetchMaxBytes = %d, want %d", got, want)
	}
	if got, want := cfg.RateLimit, (config.Rate{Count: 10, Per: time.Second}); got != want {
		t.Errorf("RateLimit = %+v, want %+v", got, want)
	}
	if got, want := len(cfg.CORSOrigins), 2; got != want {
		t.Errorf("CORSOrigins = %v, want %d entries", cfg.CORSOrigins, want)
	}
	if got, want := len(cfg.FetchAllowlist), 1; got != want {
		t.Errorf("FetchAllowlist = %v, want %d entries", cfg.FetchAllowlist, want)
	}
}

func TestAuthorizeKey(t *testing.T) {
	t.Parallel()

	cfg, _, err := config.Load(env(map[string]string{
		"BARQR_API_KEYS": "alpha, bravo",
	}))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if got, want := cfg.APIKeyCount(), 2; got != want {
		t.Fatalf("APIKeyCount() = %d, want %d", got, want)
	}
	for _, key := range []string{"alpha", "bravo"} {
		if !cfg.AuthorizeKey(key) {
			t.Errorf("AuthorizeKey(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"", "charlie", "alpha ", "ALPHA"} {
		if cfg.AuthorizeKey(key) {
			t.Errorf("AuthorizeKey(%q) = true, want false", key)
		}
	}
}

// TestRedactedHidesKeys is the regression test for the one thing in this
// package that must never leak: the plaintext API keys.
func TestRedactedHidesKeys(t *testing.T) {
	t.Parallel()

	const secret = "super-secret-key"
	cfg, _, err := config.Load(env(map[string]string{"BARQR_API_KEYS": secret}))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	out := cfg.Redacted()
	if strings.Contains(out, secret) {
		t.Fatal("Redacted() leaked the API key")
	}
	if !strings.Contains(out, "BARQR_API_KEYS=<redacted: 1 key(s)>") {
		t.Errorf("Redacted() = %q, want a redaction placeholder for BARQR_API_KEYS", out)
	}
	for _, key := range []string{"BARQR_BIND", "BARQR_PORT", "BARQR_LOG_LEVEL"} {
		if !strings.Contains(out, key+"=") {
			t.Errorf("Redacted() omits %s", key)
		}
	}
}
