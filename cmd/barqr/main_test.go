package main

import (
	"bytes"
	"strings"
	"testing"
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
