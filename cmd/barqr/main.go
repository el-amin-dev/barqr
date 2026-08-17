// Command barqr renders QR codes and barcodes over HTTP.
//
// It is a single stateless binary with three subcommands:
//
//	barqr serve [--print-config]   run the HTTP server
//	barqr check-config             validate the environment and exit
//	barqr version                  print build identity
//
// All configuration comes from BARQR_* environment variables; see
// docs/DEPLOY.md for the reference table.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/el-amin-dev/barqr/internal/config"
	"github.com/el-amin-dev/barqr/internal/httpapi"
	"github.com/el-amin-dev/barqr/internal/version"
)

// Process exit codes. They are part of the operational contract: an
// orchestrator distinguishes "this deployment is misconfigured, do not retry"
// from "the operator typed the wrong command".
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches a subcommand. It takes its streams as arguments so the whole
// command surface stays testable without touching the real process.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}

	switch cmd := args[0]; cmd {
	case "serve":
		return cmdServe(args[1:], stdout, stderr)
	case "check-config":
		return cmdCheckConfig(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, version.Get())
		return exitOK
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "barqr: unknown subcommand %q\n\n", cmd)
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `barqr — QR codes and barcodes over HTTP.

Usage:
  barqr serve [--print-config]   run the HTTP server
  barqr check-config             validate BARQR_* configuration and exit
  barqr version                  print build identity

Configuration is read from BARQR_* environment variables only.
Run "barqr check-config" to see the effective configuration.
`)
}

// cmdServe runs the HTTP server until SIGINT or SIGTERM, then drains.
func cmdServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	printConfig := fs.Bool("print-config", false,
		"print the effective configuration with secrets redacted, then exit")
	if code, done := parseFlags(fs, args, stdout, stderr); done {
		return code
	}

	cfg, warns, err := config.Load(os.Environ())
	if err != nil {
		reportConfigError(stderr, warns, err)
		return exitFailure
	}

	if *printConfig {
		fmt.Fprint(stdout, cfg.Redacted())
		return exitOK
	}

	log := newLogger(cfg, stdout)
	for _, w := range warns {
		log.Warn(w)
	}
	log.Info("starting",
		slog.String("version", version.Version),
		slog.String("commit", version.Commit))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := httpapi.New(cfg, log).Run(ctx); err != nil {
		log.Error("server stopped", slog.String("error", err.Error()))
		return exitFailure
	}

	log.Info("stopped")
	return exitOK
}

// parseFlags parses a subcommand's flags, distinguishing an explicit help
// request from a mistake.
//
// flag.ContinueOnError returns ErrHelp for -h, and treating that as a usage
// error meant `barqr serve --help` exited 2 and wrote to stderr while
// `barqr help` exited 0 and wrote to stdout — the same question answered two
// different ways depending on which spelling you reached for.
//
// The flag package writes usage during Parse, so its output is discarded and
// reprinted here once the outcome is known: help belongs on stdout, a mistake
// on stderr.
//
// done reports whether the caller should return code immediately.
func parseFlags(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) (code int, done bool) {
	fs.SetOutput(io.Discard)

	err := fs.Parse(args)
	switch {
	case err == nil:
		return exitOK, false

	case errors.Is(err, flag.ErrHelp):
		fs.SetOutput(stdout)
		fs.Usage()
		return exitOK, true

	default:
		fs.SetOutput(stderr)
		fmt.Fprintf(stderr, "barqr: %s\n\n", err)
		fs.Usage()
		return exitUsage, true
	}
}

// cmdCheckConfig validates the environment without binding a port. It is the
// twelve-factor admin process: safe to run in an init container or a CI job to
// catch a bad deployment before it takes traffic.
func cmdCheckConfig(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check-config", flag.ContinueOnError)
	if code, done := parseFlags(fs, args, stdout, stderr); done {
		return code
	}

	cfg, warns, err := config.Load(os.Environ())
	if err != nil {
		reportConfigError(stderr, warns, err)
		return exitFailure
	}

	for _, w := range warns {
		fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	fmt.Fprint(stdout, cfg.Redacted())
	return exitOK
}

// reportConfigError prints configuration failures as plain lines on stderr.
//
// The structured logger is deliberately not used: its level and format come
// from the very configuration that failed to parse.
//
// Warnings are printed alongside the errors rather than discarded. The whole
// point of aggregating problems is that an operator sees all of them in one
// boot, and a typo that made a variable be ignored is exactly the kind of
// thing they need to know about while they are already editing the file.
func reportConfigError(stderr io.Writer, warns []string, err error) {
	fmt.Fprintln(stderr, "barqr: configuration error, refusing to start")
	for _, line := range splitJoined(err) {
		fmt.Fprintf(stderr, "  - %s\n", line)
	}
	for _, w := range warns {
		fmt.Fprintf(stderr, "  - warning: %s\n", w)
	}
}

// splitJoined unwraps the errors.Join tree produced by config.Load into one
// line per problem.
func splitJoined(err error) []string {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok { //nolint:errorlint // inspecting this node, not the chain
		var out []string
		for _, e := range joined.Unwrap() {
			out = append(out, splitJoined(e)...)
		}
		return out
	}
	return []string{err.Error()}
}

// newLogger builds the process logger from configuration: structured, to
// stdout, never to a file (twelve-factor XI).
func newLogger(cfg *config.Config, stdout io.Writer) *slog.Logger {
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(stdout, opts)
	} else {
		handler = slog.NewJSONHandler(stdout, opts)
	}

	return slog.New(handler).With(slog.String("service", version.Name))
}
