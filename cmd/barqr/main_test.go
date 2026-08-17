package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/config"
	"github.com/el-amin-dev/barqr/internal/version"
)

func TestRunDispatch(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "version prints build identity",
			args:       []string{"version"},
			wantCode:   exitOK,
			wantStdout: "barqr",
		},
		{
			name:       "help goes to stdout",
			args:       []string{"help"},
			wantCode:   exitOK,
			wantStdout: "barqr check-config",
		},
		{
			name:       "no arguments is a usage error",
			args:       nil,
			wantCode:   exitUsage,
			wantStderr: "Usage:",
		},
		{
			name:       "unknown subcommand is a usage error",
			args:       []string{"render"},
			wantCode:   exitUsage,
			wantStderr: `unknown subcommand "render"`,
		},
		{
			name:       "unknown flag is a usage error",
			args:       []string{"serve", "--nope"},
			wantCode:   exitUsage,
			wantStderr: "flag provided but not defined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			if got := run(tt.args, &stdout, &stderr); got != tt.wantCode {
				t.Errorf("run(%v) = %d, want %d", tt.args, got, tt.wantCode)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestCheckConfigRejectsInsecureBind(t *testing.T) {
	t.Setenv("BARQR_BIND", "0.0.0.0")

	var stdout, stderr bytes.Buffer
	if got := run([]string{"check-config"}, &stdout, &stderr); got != exitFailure {
		t.Fatalf("run(check-config) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stderr.String(), "refusing to start") {
		t.Errorf("stderr = %q, want it to explain the refusal", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing on failure", stdout.String())
	}
}

func TestCheckConfigPrintsRedactedConfig(t *testing.T) {
	const secret = "not-in-the-output"
	t.Setenv("BARQR_API_KEYS", secret)

	var stdout, stderr bytes.Buffer
	if got := run([]string{"check-config"}, &stdout, &stderr); got != exitOK {
		t.Fatalf("run(check-config) = %d, want %d (stderr: %s)", got, exitOK, stderr.String())
	}
	if strings.Contains(stdout.String(), secret) {
		t.Fatal("check-config leaked the API key")
	}
	if !strings.Contains(stdout.String(), "BARQR_BIND=") {
		t.Errorf("stdout = %q, want the effective configuration", stdout.String())
	}
}

func TestServePrintConfigDoesNotBind(t *testing.T) {
	t.Setenv("BARQR_PORT", "3000")

	var stdout, stderr bytes.Buffer
	if got := run([]string{"serve", "--print-config"}, &stdout, &stderr); got != exitOK {
		t.Fatalf("run(serve --print-config) = %d, want %d (stderr: %s)", got, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "BARQR_PORT=3000") {
		t.Errorf("stdout = %q, want the effective configuration", stdout.String())
	}
}

// TestVersionReportsBuildIdentity pins the fields an operator pastes into a
// bug report: what was built, from where, with which toolchain, for which
// platform.
func TestVersionReportsBuildIdentity(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if got := run([]string{"version"}, &stdout, &stderr); got != exitOK {
		t.Fatalf("run(version) = %d, want %d", got, exitOK)
	}

	for _, want := range []string{
		version.Name,
		version.Version,
		version.Commit,
		runtime.Version(),
		runtime.GOOS + "/" + runtime.GOARCH,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing on success", stderr.String())
	}
}

// TestHelpFlagsPrintUsageToStdout: asking for help is not an error, so the
// text goes to stdout and the exit code is zero.
func TestHelpFlagsPrintUsageToStdout(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"help", "-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			if got := run([]string{arg}, &stdout, &stderr); got != exitOK {
				t.Fatalf("run(%q) = %d, want %d", arg, got, exitOK)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Errorf("stdout = %q, want the usage text", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want nothing on success", stderr.String())
			}
		})
	}
}

// TestUsageErrorsKeepStdoutClean is the other half of the contract: a misuse
// exits 2 and says so on stderr, leaving stdout empty so that a caller piping
// `barqr version` or `barqr check-config` never parses an error message.
func TestUsageErrorsKeepStdoutClean(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{
			name:       "no arguments",
			args:       nil,
			wantStderr: "Usage:",
		},
		{
			name:       "unknown subcommand",
			args:       []string{"generate"},
			wantStderr: `unknown subcommand "generate"`,
		},
		{
			name:       "unknown serve flag",
			args:       []string{"serve", "--daemonize"},
			wantStderr: "flag provided but not defined: -daemonize",
		},
		{
			name:       "unknown check-config flag",
			args:       []string{"check-config", "--strict"},
			wantStderr: "flag provided but not defined: -strict",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr); got != exitUsage {
				t.Errorf("run(%v) = %d, want %d", tt.args, got, exitUsage)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing on a usage error", stdout.String())
			}
		})
	}
}

// TestCheckConfigReportsEveryProblem is the deployment-triage guarantee: an
// operator who set three variables wrongly must see all three at once, one per
// line, instead of fixing one and restarting to discover the next.
func TestCheckConfigReportsEveryProblem(t *testing.T) {
	t.Setenv("BARQR_PORT", "0")
	t.Setenv("BARQR_LOG_LEVEL", "chatty")
	t.Setenv("BARQR_MAX_BODY", "enormous")

	var stdout, stderr bytes.Buffer
	if got := run([]string{"check-config"}, &stdout, &stderr); got != exitFailure {
		t.Fatalf("run(check-config) = %d, want %d", got, exitFailure)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing on failure", stdout.String())
	}

	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	if want := "barqr: configuration error, refusing to start"; lines[0] != want {
		t.Errorf("first line = %q, want %q", lines[0], want)
	}

	var problems []string
	for _, line := range lines[1:] {
		if after, ok := strings.CutPrefix(line, "  - "); ok {
			problems = append(problems, after)
		}
	}
	if len(problems) != 3 {
		t.Fatalf("got %d problem lines, want 3; stderr:\n%s", len(problems), stderr.String())
	}

	for _, key := range []string{"BARQR_PORT", "BARQR_LOG_LEVEL", "BARQR_MAX_BODY"} {
		if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, key) }) {
			t.Errorf("no problem line names %s; stderr:\n%s", key, stderr.String())
		}
	}
}

// TestCheckConfigWarnsAboutUnknownVariables: a typo is not fatal, but it must
// be visible, and it belongs on stderr so that stdout stays a clean dump of
// the effective configuration.
func TestCheckConfigWarnsAboutUnknownVariables(t *testing.T) {
	t.Setenv("BARQR_API_KEY", "singular-is-a-typo")

	var stdout, stderr bytes.Buffer
	if got := run([]string{"check-config"}, &stdout, &stderr); got != exitOK {
		t.Fatalf("run(check-config) = %d, want %d (stderr: %s)", got, exitOK, stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning: unknown environment variable BARQR_API_KEY") {
		t.Errorf("stderr = %q, want the typo reported", stderr.String())
	}
	if strings.Contains(stdout.String(), "warning:") {
		t.Errorf("stdout = %q, want warnings kept off stdout", stdout.String())
	}
}

// TestCheckConfigNeverPrintsAPIKeys asserts the redaction guarantee at the CLI
// boundary rather than in the config package: whatever `barqr check-config`
// emits is safe to paste into an issue.
func TestCheckConfigNeverPrintsAPIKeys(t *testing.T) {
	const first, second = "sk-live-first-key", "sk-live-second-key"
	t.Setenv("BARQR_API_KEYS", first+","+second)

	var stdout, stderr bytes.Buffer
	if got := run([]string{"check-config"}, &stdout, &stderr); got != exitOK {
		t.Fatalf("run(check-config) = %d, want %d (stderr: %s)", got, exitOK, stderr.String())
	}

	for name, stream := range map[string]string{"stdout": stdout.String(), "stderr": stderr.String()} {
		for _, secret := range []string{first, second} {
			if strings.Contains(stream, secret) {
				t.Errorf("%s leaked an API key: %q", name, stream)
			}
		}
	}
	if want := "BARQR_API_KEYS=<redacted: 2 key(s)>"; !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
	}
}

func TestReportConfigErrorPrintsOneLinePerProblem(t *testing.T) {
	t.Parallel()

	const header = "barqr: configuration error, refusing to start"

	tests := []struct {
		name  string
		err   error
		warns []string
		want  []string
	}{
		{
			name: "a single error",
			err:  errors.New("BARQR_PORT: expected an integer"),
			want: []string{header, "  - BARQR_PORT: expected an integer"},
		},
		{
			name: "a joined tree of several",
			err: errors.Join(
				errors.New("first"),
				errors.New("second"),
				errors.New("third"),
			),
			want: []string{header, "  - first", "  - second", "  - third"},
		},
		{
			// config.Load joins flat today, but errors.Join nests as soon as
			// one validator aggregates its own findings. Flattening must
			// survive that, or a whole subtree collapses into one line.
			name: "a nested join",
			err: errors.Join(
				errors.New("outer"),
				errors.Join(errors.New("inner one"), errors.Join(errors.New("inner two"))),
			),
			want: []string{header, "  - outer", "  - inner one", "  - inner two"},
		},
		{
			// Warnings used to be dropped whenever the configuration also had
			// an error, so an operator fixing a bad port was never told that a
			// typo was silently making another variable be ignored. Seeing
			// every problem in one boot is the whole point.
			name:  "warnings accompany the errors",
			err:   errors.New("BARQR_PORT: not a number"),
			warns: []string{"unknown environment variable BARQR_API_KEY is ignored"},
			want: []string{
				header,
				"  - BARQR_PORT: not a number",
				"  - warning: unknown environment variable BARQR_API_KEY is ignored",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer
			reportConfigError(&stderr, tt.warns, tt.err)

			got := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
			if !slices.Equal(got, tt.want) {
				t.Errorf("reportConfigError wrote\n%#v\nwant\n%#v", got, tt.want)
			}
		})
	}
}

func TestNewLoggerFiltersByLevel(t *testing.T) {
	t.Parallel()

	// Each logger emits one record per severity; the test then asserts
	// exactly which of them survived the handler's level filter.
	tests := []struct {
		name  string
		level string
		want  []string
	}{
		{name: "debug keeps everything", level: "debug", want: []string{"d", "i", "w", "e"}},
		{name: "info drops debug", level: "info", want: []string{"i", "w", "e"}},
		{name: "warn drops info", level: "warn", want: []string{"w", "e"}},
		{name: "error keeps only errors", level: "error", want: []string{"e"}},
		{name: "an empty level falls back to info", level: "", want: []string{"i", "w", "e"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			log := newLogger(&config.Config{LogLevel: tt.level}, &buf)
			log.Debug("d")
			log.Info("i")
			log.Warn("w")
			log.Error("e")

			var got []string
			for _, rec := range decodeJSONLines(t, buf.String()) {
				msg, ok := rec["msg"].(string)
				if !ok {
					t.Fatalf("record %v has no msg field", rec)
				}
				got = append(got, msg)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("emitted %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNewLoggerDefaultsToJSONWithService pins the shape a log pipeline parses:
// one JSON object per line, every one tagged with the service that wrote it.
func TestNewLoggerDefaultsToJSONWithService(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	newLogger(&config.Config{}, &buf).Info("hello", slog.String("k", "v"))

	records := decodeJSONLines(t, buf.String())
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}

	for key, want := range map[string]string{
		"service": version.Name,
		"level":   "INFO",
		"msg":     "hello",
		"k":       "v",
	} {
		if got, _ := records[0][key].(string); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestNewLoggerHonoursTextFormat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	newLogger(&config.Config{LogFormat: "text", LogLevel: "warn"}, &buf).Warn("careful")

	got := buf.String()
	if strings.HasPrefix(got, "{") {
		t.Fatalf("got JSON %q, want text", got)
	}
	for _, want := range []string{"level=WARN", `msg=careful`, "service=" + version.Name} {
		if !strings.Contains(got, want) {
			t.Errorf("log line = %q, want it to contain %q", got, want)
		}
	}
}

// decodeJSONLines parses the newline-delimited JSON a slog handler produced.
func decodeJSONLines(t *testing.T, s string) []map[string]any {
	t.Helper()

	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSuffix(s, "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decoding log line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// TestSubcommandHelpExitsCleanly covers the distinction parseFlags exists to
// make: an explicit -h is a request, not a mistake.
//
// Before this, `barqr serve --help` exited 2 and wrote to stderr while
// `barqr help` exited 0 and wrote to stdout — the same question answered two
// different ways depending on which spelling you reached for.
func TestSubcommandHelpExitsCleanly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"serve -h", []string{"serve", "-h"}, "print-config"},
		{"serve --help", []string{"serve", "--help"}, "print-config"},
		{"check-config -h", []string{"check-config", "-h"}, "check-config"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr); got != exitOK {
				t.Errorf("run(%v) = %d, want %d", tt.args, got, exitOK)
			}
			if !strings.Contains(stdout.String(), tt.want) {
				t.Errorf("stdout = %q, want it to mention %q and go to stdout, not stderr",
					stdout.String(), tt.want)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want help on stdout only", stderr.String())
			}
		})
	}
}

// TestUnknownFlagIsStillAUsageError guards the other side of that split: a
// genuine mistake must not be quietly rewarded with a clean exit.
func TestUnknownFlagIsStillAUsageError(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"serve", "--nope"},
		{"check-config", "--nope"},
	} {
		var stdout, stderr bytes.Buffer
		if got := run(args, &stdout, &stderr); got != exitUsage {
			t.Errorf("run(%v) = %d, want %d", args, got, exitUsage)
		}
		if stderr.Len() == 0 {
			t.Errorf("run(%v) said nothing on stderr", args)
		}
	}
}
