// Package config loads, validates, and redacts the barqr runtime
// configuration.
//
// Configuration comes from the process environment and nothing else
// (twelve-factor III): there are no configuration files and no flags that
// change behaviour. Load is called exactly once at boot; the resulting
// Config is treated as immutable for the lifetime of the process.
//
// Two rules are absolute:
//
//   - An invalid value is fatal. barqr never silently falls back to a
//     default, because a silently ignored BARQR_AUTH_MODE is a security
//     incident waiting to happen.
//   - An unrecognised BARQR_* variable is a warning, so that a typo such as
//     BARQR_API_KEY (singular) is visible in the boot logs instead of
//     leaving the service unauthenticated.
package config

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Prefix is the environment-variable namespace owned by barqr.
const Prefix = "BARQR_"

// Sentinel errors returned (wrapped) by Load.
var (
	// ErrInvalid marks a variable whose value could not be parsed or fell
	// outside the permitted range.
	ErrInvalid = errors.New("invalid configuration value")
	// ErrInsecure marks a combination of individually valid values that
	// would expose barqr to an unintended network. These are always fatal.
	ErrInsecure = errors.New("insecure configuration")
)

// AuthMode selects how incoming requests are authenticated.
type AuthMode string

// Supported authentication modes.
const (
	// AuthRequired rejects every request that does not present a configured
	// API key. It is the default.
	AuthRequired AuthMode = "required"
	// AuthOpen disables authentication entirely. Only safe on a loopback
	// bind or behind an authenticating proxy.
	AuthOpen AuthMode = "open"
)

// ScannabilityMode selects what barqr does when a requested style is likely
// to produce a code that scanners struggle with.
type ScannabilityMode string

// Supported scannability modes.
const (
	// ScannabilityOff performs no analysis.
	ScannabilityOff ScannabilityMode = "off"
	// ScannabilityWarn renders the code and reports the risk in a response
	// header. It is the default.
	ScannabilityWarn ScannabilityMode = "warn"
	// ScannabilityStrict rejects the request instead of rendering.
	ScannabilityStrict ScannabilityMode = "strict"
)

// Rate is a request allowance: Count events per Per window.
type Rate struct {
	Count int
	Per   time.Duration
}

// Unlimited reports whether the rate imposes no limit at all.
func (r Rate) Unlimited() bool { return r.Count <= 0 }

// String renders the rate in the same `<count>/<unit>` form it is parsed from.
func (r Rate) String() string {
	if r.Unlimited() {
		return "unlimited"
	}
	switch r.Per {
	case time.Second:
		return strconv.Itoa(r.Count) + "/s"
	case time.Minute:
		return strconv.Itoa(r.Count) + "/min"
	case time.Hour:
		return strconv.Itoa(r.Count) + "/hour"
	default:
		return strconv.Itoa(r.Count) + "/" + r.Per.String()
	}
}

// Config is the fully parsed, validated runtime configuration.
//
// Every field corresponds to exactly one BARQR_* environment variable; see
// docs/DEPLOY.md for the reference table. API keys are deliberately absent:
// they are hashed at Load time and reachable only through AuthorizeKey.
type Config struct {
	// Network.
	Bind string
	Port int

	// Authentication.
	AuthMode AuthMode

	// Request limits.
	MaxBody        int64
	RequestTimeout time.Duration
	RateLimit      Rate
	Concurrency    int
	ShutdownGrace  time.Duration

	// Rendering defaults and caps.
	DefaultFormat string
	DefaultECC    string
	MaxCanvasPx   int64
	MaxBatchItems int

	// Outbound fetching (logos, backgrounds).
	AllowRemoteFetch bool
	FetchAllowlist   []string
	FetchTimeout     time.Duration
	FetchMaxBytes    int64

	// Behaviour.
	// Docs enables the browser documentation UI at / and /v1/docs. It is on by
	// default because a service nobody can learn to use is not much of a
	// service, and off is one variable away for a deployment that wants no
	// HTML surface at all.
	Docs               bool
	StrictScannability ScannabilityMode
	CORSOrigins        []string
	Metrics            bool
	LogLevel           string
	LogFormat          string
	PresetsPath        string
	Dynamic            bool

	// UnderstandOpenBind is the explicit operator acknowledgement required
	// to run unauthenticated on a wildcard bind.
	UnderstandOpenBind bool

	// apiKeyHashes holds SHA-256 digests of the configured API keys. The
	// plaintext keys are discarded by Load and never stored, logged, or
	// printed.
	apiKeyHashes [][sha256.Size]byte

	// effective records the raw string value used for every known key after
	// defaults were applied. It backs Redacted and nothing else.
	effective map[string]string
}

// spec lists every environment variable barqr understands, in the order used
// by Redacted, together with its default. It is the single source of truth
// for both defaulting and unknown-key detection, so the two cannot drift.
var spec = []struct{ Key, Default string }{
	{"BARQR_BIND", "127.0.0.1"},
	{"BARQR_PORT", "3000"},
	{"BARQR_AUTH_MODE", string(AuthRequired)},
	{"BARQR_API_KEYS", ""},
	{"BARQR_MAX_BODY", "2MB"},
	{"BARQR_REQUEST_TIMEOUT", "10s"},
	{"BARQR_RATE_LIMIT", "120/min"},
	{"BARQR_CONCURRENCY", "8"},
	{"BARQR_SHUTDOWN_GRACE", "15s"},
	{"BARQR_DEFAULT_FORMAT", "png"},
	{"BARQR_DEFAULT_ECC", "M"},
	{"BARQR_MAX_CANVAS_PX", "25000000"},
	{"BARQR_MAX_BATCH_ITEMS", "1000"},
	{"BARQR_ALLOW_REMOTE_FETCH", "false"},
	{"BARQR_FETCH_ALLOWLIST", ""},
	{"BARQR_FETCH_TIMEOUT", "3s"},
	{"BARQR_FETCH_MAX_BYTES", "2MB"},
	{"BARQR_STRICT_SCANNABILITY", string(ScannabilityWarn)},
	{"BARQR_CORS_ORIGINS", ""},
	{"BARQR_METRICS", "true"},
	{"BARQR_LOG_LEVEL", "info"},
	{"BARQR_LOG_FORMAT", "json"},
	{"BARQR_PRESETS_PATH", ""},
	{"BARQR_DOCS", "true"},
	{"BARQR_DYNAMIC", "false"},
	{"BARQR_I_UNDERSTAND_OPEN_BIND", "false"},
}

// Keys lists every environment variable barqr reads, in the order Redacted
// prints them.
//
// It is exported so the documentation can be checked against it: a variable
// the binary reads but the reference table omits is a setting nobody can
// discover, and one the table documents but the binary ignores is worse,
// because somebody will set it and expect an effect.
func Keys() []string {
	out := make([]string, 0, len(spec))
	for _, s := range spec {
		out = append(out, s.Key)
	}
	return out
}

// secretKeys are never rendered by Redacted.
var secretKeys = map[string]bool{"BARQR_API_KEYS": true}

// outputFormats are the writers barqr can be asked to default to. Formats
// land here as their writers are implemented; the list is validated at boot
// so that BARQR_DEFAULT_FORMAT can never name a writer that does not exist.
var outputFormats = []string{
	"png", "svg", "jpeg", "webp", "pdf",
	"ascii", "unicode", "ansi", "json", "datauri", "txt",
}

// eccLevels are the QR error-correction levels, weakest to strongest.
var eccLevels = []string{"L", "M", "Q", "H"}

// Load parses environ — in the "KEY=VALUE" form returned by os.Environ — into
// a Config.
//
// It returns the configuration, a slice of non-fatal warnings suitable for
// logging at boot, and an error. The error aggregates every problem found so
// that an operator fixing a misconfigured deployment sees the whole list at
// once rather than one item per restart. A non-nil error means the process
// must not start.
func Load(environ []string) (*Config, []string, error) {
	l := newLoader(environ)

	cfg := &Config{
		Bind:               l.str("BARQR_BIND"),
		Port:               l.intIn("BARQR_PORT", 1, 65535),
		AuthMode:           AuthMode(l.enum("BARQR_AUTH_MODE", string(AuthRequired), string(AuthOpen))),
		MaxBody:            l.bytes("BARQR_MAX_BODY", 1),
		RequestTimeout:     l.duration("BARQR_REQUEST_TIMEOUT", time.Millisecond),
		RateLimit:          l.rate("BARQR_RATE_LIMIT"),
		Concurrency:        l.intIn("BARQR_CONCURRENCY", 1, 1<<20),
		ShutdownGrace:      l.duration("BARQR_SHUTDOWN_GRACE", 0),
		DefaultFormat:      l.enum("BARQR_DEFAULT_FORMAT", outputFormats...),
		DefaultECC:         l.enum("BARQR_DEFAULT_ECC", eccLevels...),
		MaxCanvasPx:        l.int64In("BARQR_MAX_CANVAS_PX", 1, 1<<40),
		MaxBatchItems:      l.intIn("BARQR_MAX_BATCH_ITEMS", 1, 1<<20),
		AllowRemoteFetch:   l.boolean("BARQR_ALLOW_REMOTE_FETCH"),
		FetchAllowlist:     l.list("BARQR_FETCH_ALLOWLIST"),
		FetchTimeout:       l.duration("BARQR_FETCH_TIMEOUT", time.Millisecond),
		FetchMaxBytes:      l.bytes("BARQR_FETCH_MAX_BYTES", 1),
		Docs:               l.boolean("BARQR_DOCS"),
		StrictScannability: ScannabilityMode(l.enum("BARQR_STRICT_SCANNABILITY", string(ScannabilityOff), string(ScannabilityWarn), string(ScannabilityStrict))),
		CORSOrigins:        l.list("BARQR_CORS_ORIGINS"),
		Metrics:            l.boolean("BARQR_METRICS"),
		LogLevel:           l.enum("BARQR_LOG_LEVEL", "debug", "info", "warn", "error"),
		LogFormat:          l.enum("BARQR_LOG_FORMAT", "json", "text"),
		PresetsPath:        l.str("BARQR_PRESETS_PATH"),
		Dynamic:            l.boolean("BARQR_DYNAMIC"),
		UnderstandOpenBind: l.boolean("BARQR_I_UNDERSTAND_OPEN_BIND"),
	}

	cfg.apiKeyHashes = hashKeys(l.list("BARQR_API_KEYS"))
	cfg.effective = l.effective

	// Only check the security invariants once every individual value is
	// known good; otherwise a bad BARQR_AUTH_MODE would produce a
	// misleading "insecure bind" error on top of the real problem.
	if len(l.errs) == 0 {
		l.errs = append(l.errs, cfg.checkSecurity()...)
	}

	if len(l.errs) > 0 {
		return nil, l.warns, errors.Join(l.errs...)
	}

	l.warns = append(l.warns, cfg.unimplemented()...)
	return cfg, l.warns, nil
}

// unimplemented reports settings this build parses but cannot act on.
//
// Each of these is a valid, documented variable whose feature is not in the
// binary yet. Accepting one silently would be exactly the failure mode barqr
// refuses everywhere else — an option that looks like it worked and did
// nothing — so the operator is told at boot rather than left to wonder why
// their logo never appears.
func (c *Config) unimplemented() []string {
	var warns []string

	// Remote fetching is implemented, but an empty allowlist means nothing is
	// reachable — which is the safe default and also, for someone who has just
	// switched the feature on, almost certainly not what they meant.
	if c.AllowRemoteFetch && len(c.FetchAllowlist) == 0 {
		warns = append(warns, "BARQR_ALLOW_REMOTE_FETCH=true with an empty "+
			"BARQR_FETCH_ALLOWLIST fetches nothing: the allowlist is exact-match and "+
			"deny-by-default, so name the hosts you intend to allow.")
	}
	if c.Dynamic {
		warns = append(warns, "BARQR_DYNAMIC=true is not honoured by this build: the "+
			"dynamic-code module is not implemented and /v1/dynamic is not routed.")
	}

	return warns
}

// checkSecurity enforces the deny-by-default posture from docs/SECURITY.md.
// Both rules are startup-fatal by design: barqr would rather fail to boot
// than serve unauthenticated code generation to a network it cannot see.
func (c *Config) checkSecurity() []error {
	var errs []error

	if !isLoopback(c.Bind) && c.AuthMode == AuthRequired && len(c.apiKeyHashes) == 0 {
		errs = append(errs, fmt.Errorf(
			"%w: BARQR_BIND=%q is not loopback and BARQR_AUTH_MODE=required, but BARQR_API_KEYS is empty; "+
				"set BARQR_API_KEYS to one or more keys, or bind to 127.0.0.1",
			ErrInsecure, c.Bind))
	}

	// Not just a wildcard: BARQR_BIND=10.0.1.7 with open auth is unauthenticated
	// code generation on the LAN, which is the same exposure with a narrower
	// blast radius. The predicate is "not loopback", so both are caught.
	if c.AuthMode == AuthOpen && !isLoopback(c.Bind) && !c.UnderstandOpenBind {
		reach := "that network"
		if isWildcard(c.Bind) {
			reach = "every network interface"
		}
		errs = append(errs, fmt.Errorf(
			"%w: BARQR_AUTH_MODE=open on non-loopback bind %q would serve unauthenticated "+
				"requests to %s; set BARQR_I_UNDERSTAND_OPEN_BIND=true to accept this, "+
				"or use BARQR_AUTH_MODE=required",
			ErrInsecure, c.Bind, reach))
	}

	return errs
}

// Addr is the host:port the HTTP server listens on.
func (c *Config) Addr() string {
	return net.JoinHostPort(c.Bind, strconv.Itoa(c.Port))
}

// APIKeyCount reports how many API keys were configured. The keys themselves
// are not retrievable.
func (c *Config) APIKeyCount() int { return len(c.apiKeyHashes) }

// AuthorizeKey reports whether presented matches a configured API key.
//
// The comparison runs against every configured key and uses
// subtle.ConstantTimeCompare, so neither the outcome nor which key matched
// is observable through response timing.
func (c *Config) AuthorizeKey(presented string) bool {
	sum := sha256.Sum256([]byte(presented))
	match := 0
	for i := range c.apiKeyHashes {
		match |= subtle.ConstantTimeCompare(sum[:], c.apiKeyHashes[i][:])
	}
	return match == 1
}

// KeyID returns a stable, non-secret identifier for a presented key: its
// one-based position in BARQR_API_KEYS, rendered as "k<N>". Logs and rate
// limit buckets use this so that neither ever contains the key itself.
//
// It returns an empty string for a key that is not configured. Like
// AuthorizeKey it checks every entry without an early return, so the identity
// of the matching key does not leak through timing either.
func (c *Config) KeyID(presented string) string {
	sum := sha256.Sum256([]byte(presented))
	idx := -1
	for i := range c.apiKeyHashes {
		if subtle.ConstantTimeCompare(sum[:], c.apiKeyHashes[i][:]) == 1 {
			idx = i
		}
	}
	if idx < 0 {
		return ""
	}
	return "k" + strconv.Itoa(idx+1)
}

// Redacted renders the effective configuration as sorted KEY=value lines with
// secrets replaced by a placeholder. It is safe to print or log, and backs
// both `barqr check-config` and `barqr serve --print-config`.
func (c *Config) Redacted() string {
	var b strings.Builder
	for _, s := range spec {
		v := c.effective[s.Key]
		switch {
		case secretKeys[s.Key]:
			v = fmt.Sprintf("<redacted: %d key(s)>", c.APIKeyCount())
		case v == "":
			v = "<empty>"
		}
		fmt.Fprintf(&b, "%s=%s\n", s.Key, v)
	}
	return b.String()
}

// hashKeys digests each API key so the plaintext never reaches the Config.
func hashKeys(keys []string) [][sha256.Size]byte {
	if len(keys) == 0 {
		return nil
	}
	out := make([][sha256.Size]byte, 0, len(keys))
	for _, k := range keys {
		out = append(out, sha256.Sum256([]byte(k)))
	}
	return out
}

// isLoopback reports whether host addresses only the local machine.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isWildcard reports whether host means "every interface".
func isWildcard(host string) bool {
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// loader accumulates parse errors and warnings so that Load can report every
// problem in one pass instead of failing on the first bad variable.
type loader struct {
	raw       map[string]string
	effective map[string]string
	errs      []error
	warns     []string
}

func newLoader(environ []string) *loader {
	l := &loader{
		raw:       make(map[string]string, len(environ)),
		effective: make(map[string]string, len(spec)),
	}

	known := make(map[string]bool, len(spec))
	for _, s := range spec {
		known[s.Key] = true
	}

	var unknown []string
	for _, kv := range environ {
		key, val, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(key, Prefix) {
			continue
		}
		l.raw[key] = val
		if !known[key] {
			unknown = append(unknown, key)
		}
	}

	sort.Strings(unknown)
	for _, key := range unknown {
		l.warns = append(l.warns, fmt.Sprintf(
			"unknown environment variable %s is ignored (check for a typo)", key))
	}

	for _, s := range spec {
		if v, ok := l.raw[s.Key]; ok {
			l.effective[s.Key] = v
			continue
		}
		l.effective[s.Key] = s.Default
	}

	return l
}

// fail records an invalid value. The raw value is included because operators
// need to see what was actually set, and BARQR_* values are non-secret with
// the single exception of BARQR_API_KEYS, which is never parsed through here.
func (l *loader) fail(key, reason string) {
	l.errs = append(l.errs, fmt.Errorf("%w: %s=%q: %s", ErrInvalid, key, l.effective[key], reason))
}

// str returns a trimmed string value. Everything else trims, and BARQR_BIND in
// particular must: a stray space makes it neither loopback nor wildcard, which
// would slip past the security invariants before failing confusingly at listen.
func (l *loader) str(key string) string { return strings.TrimSpace(l.effective[key]) }

// list splits a comma-separated value, trimming spaces and dropping empties.
func (l *loader) list(key string) []string {
	raw := strings.Split(l.effective[key], ",")
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (l *loader) boolean(key string) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(l.effective[key]))
	if err != nil {
		l.fail(key, "expected a boolean (true, false, 1, 0)")
		return false
	}
	return v
}

func (l *loader) intIn(key string, lo, hi int) int {
	return int(l.int64In(key, int64(lo), int64(hi)))
}

func (l *loader) int64In(key string, lo, hi int64) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(l.effective[key]), 10, 64)
	if err != nil {
		l.fail(key, "expected an integer")
		return lo
	}
	if v < lo || v > hi {
		l.fail(key, fmt.Sprintf("expected a value in [%d, %d]", lo, hi))
		return lo
	}
	return v
}

func (l *loader) enum(key string, allowed ...string) string {
	v := strings.TrimSpace(l.effective[key])
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	l.fail(key, "expected one of "+strings.Join(allowed, ", "))
	return allowed[0]
}

// duration parses a Go duration and rejects anything below lo. A lo of 0
// permits a zero duration, which some settings use to mean "no limit".
func (l *loader) duration(key string, lo time.Duration) time.Duration {
	v, err := time.ParseDuration(strings.TrimSpace(l.effective[key]))
	if err != nil {
		l.fail(key, "expected a duration (e.g. 10s, 500ms, 2m)")
		return lo
	}
	if v < lo {
		l.fail(key, fmt.Sprintf("expected a duration of at least %s", lo))
		return lo
	}
	return v
}

// sizeUnits maps the suffixes accepted by bytes to their multipliers. Sizes
// are binary: 1MB is 1048576 bytes, matching how operators size containers.
var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"KIB", 1 << 10}, {"MIB", 1 << 20}, {"GIB", 1 << 30},
	{"KB", 1 << 10}, {"MB", 1 << 20}, {"GB", 1 << 30},
	{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30},
	{"B", 1},
}

// bytes parses a byte size such as "2MB", "512KiB", or a bare byte count.
func (l *loader) bytes(key string, lo int64) int64 {
	s := strings.ToUpper(strings.TrimSpace(l.effective[key]))
	mult := int64(1)
	for _, u := range sizeUnits {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.mult
			break
		}
	}

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		l.fail(key, "expected a byte size (e.g. 2MB, 512KB, 1048576)")
		return lo
	}
	if n < 0 || n > (1<<62)/mult {
		l.fail(key, "byte size is out of range")
		return lo
	}
	if n*mult < lo {
		l.fail(key, fmt.Sprintf("expected at least %d bytes", lo))
		return lo
	}
	return n * mult
}

// rateUnits maps the window suffixes accepted by rate to their durations.
var rateUnits = map[string]time.Duration{
	"s": time.Second, "sec": time.Second, "second": time.Second,
	"m": time.Minute, "min": time.Minute, "minute": time.Minute,
	"h": time.Hour, "hour": time.Hour,
}

// rate parses a "<count>/<window>" allowance such as "120/min". A count of 0
// disables rate limiting.
func (l *loader) rate(key string) Rate {
	s := strings.TrimSpace(l.effective[key])
	countStr, unitStr, ok := strings.Cut(s, "/")
	if !ok {
		l.fail(key, "expected <count>/<unit>, e.g. 120/min")
		return Rate{}
	}

	count, err := strconv.Atoi(strings.TrimSpace(countStr))
	if err != nil || count < 0 {
		l.fail(key, "expected a non-negative count before the slash")
		return Rate{}
	}

	per, known := rateUnits[strings.ToLower(strings.TrimSpace(unitStr))]
	if !known {
		l.fail(key, "expected a unit of s, min, or hour after the slash")
		return Rate{}
	}

	return Rate{Count: count, Per: per}
}
