package main

import (
	"bytes"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// serveStartTimeout bounds how long a test waits for cmdServe to bind. It is
// generous because the whole boot path — config, presets, listener — runs on a
// possibly loaded CI box, and a false failure here is worse than a slow test.
const serveStartTimeout = 15 * time.Second

// syncBuffer is a bytes.Buffer that may be written by cmdServe on one
// goroutine while the test reads it on another. The race detector runs in CI,
// so the lock is load-bearing rather than decorative.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// freePort asks the kernel for an unused loopback port and releases it again.
// The port is only a hint: it can be taken between the close and the moment
// cmdServe binds, which is why callers retry.
func freePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("releasing the reserved port: %v", err)
	}
	return port
}

// waitForListen dials addr until the connection is accepted. It reports false
// if run returned first, which means the server never got as far as listening
// and the caller should either retry on a fresh port or fail.
func waitForListen(t *testing.T, addr string, done <-chan int) bool {
	t.Helper()

	deadline := time.Now().Add(serveStartTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return false
		default:
		}

		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestServeShutsDownCleanlyOnSIGTERM drives the real serve path: bind, log,
// wait for a signal, drain, exit 0. It is the only test that raises a signal
// at the test binary itself, so it is deliberately not parallel.
func TestServeShutsDownCleanlyOnSIGTERM(t *testing.T) {
	// Take over SIGTERM for the whole test *before* anything can raise it.
	// The default disposition kills the process, and cmdServe only installs
	// its own handler part-way through boot; without this registration the
	// window between the two would be a way to lose the test binary.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	defer signal.Stop(sig)

	t.Setenv("BARQR_BIND", "127.0.0.1")
	t.Setenv("BARQR_AUTH_MODE", "open")
	t.Setenv("BARQR_METRICS", "false")
	t.Setenv("BARQR_SHUTDOWN_GRACE", "5s")
	// An unknown BARQR_* variable is a non-fatal warning, which exercises the
	// warning loop cmdServe runs before it starts listening.
	t.Setenv("BARQR_NOT_A_REAL_SETTING", "1")

	var stdout, stderr syncBuffer
	var done chan int
	var addr string

	const attempts = 3
	for attempt := 1; ; attempt++ {
		port := freePort(t)
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		t.Setenv("BARQR_PORT", strconv.Itoa(port))

		stdout.Reset()
		stderr.Reset()
		// The goroutine sends on its own copy of the channel so that a
		// retry cannot reassign the variable underneath it.
		ch := make(chan int, 1)
		done = ch
		go func() { ch <- run([]string{"serve"}, &stdout, &stderr) }()

		if waitForListen(t, addr, done) {
			break
		}
		if attempt == attempts {
			t.Fatalf("serve never listened on %s after %d attempts; output:\n%s%s",
				addr, attempts, stdout.String(), stderr.String())
		}
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("raising SIGTERM: %v", err)
	}

	select {
	case got := <-done:
		if got != exitOK {
			t.Errorf("run(serve) = %d, want %d; output:\n%s%s",
				got, exitOK, stdout.String(), stderr.String())
		}
	case <-time.After(serveStartTimeout):
		t.Fatalf("serve did not return after SIGTERM; output:\n%s%s",
			stdout.String(), stderr.String())
	}

	// The boot and drain narrative an operator reads in the logs.
	for _, want := range []string{
		`"msg":"starting"`,
		`"msg":"listening"`,
		`"msg":"shutting down"`,
		`"msg":"stopped"`,
		"BARQR_NOT_A_REAL_SETTING",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
		}
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want the logger to own the output", stderr.String())
	}
}

// TestServeReportsAFailureToBind pins the operational contract for the most
// common serve failure: the port is already taken, so the process must exit
// non-zero rather than sit there looking healthy.
func TestServeReportsAFailureToBind(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	defer func() { _ = held.Close() }()

	t.Setenv("BARQR_BIND", "127.0.0.1")
	t.Setenv("BARQR_AUTH_MODE", "open")
	t.Setenv("BARQR_METRICS", "false")
	t.Setenv("BARQR_PORT", strconv.Itoa(held.Addr().(*net.TCPAddr).Port))

	var stdout, stderr bytes.Buffer
	if got := run([]string{"serve"}, &stdout, &stderr); got != exitFailure {
		t.Fatalf("run(serve) = %d, want %d; stdout:\n%s", got, exitFailure, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"msg":"server stopped"`) {
		t.Errorf("stdout = %q, want the bind failure logged", stdout.String())
	}
}

// TestServeRejectsAMisconfiguredEnvironment covers the exit-1 path taken
// before any listener exists, and proves nothing is written to stdout when the
// process refuses to start.
func TestServeRejectsAMisconfiguredEnvironment(t *testing.T) {
	t.Setenv("BARQR_LOG_LEVEL", "verbose")

	var stdout, stderr bytes.Buffer
	if got := run([]string{"serve"}, &stdout, &stderr); got != exitFailure {
		t.Fatalf("run(serve) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stderr.String(), "BARQR_LOG_LEVEL") {
		t.Errorf("stderr = %q, want it to name the bad variable", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing on failure", stdout.String())
	}
}

// TestServePrintConfigLeavesThePortFree is the admin-process guarantee:
// --print-config resolves configuration and returns without touching the
// network, so the configured port is still bindable afterwards.
func TestServePrintConfigLeavesThePortFree(t *testing.T) {
	port := freePort(t)
	t.Setenv("BARQR_PORT", strconv.Itoa(port))

	var stdout, stderr bytes.Buffer
	if got := run([]string{"serve", "--print-config"}, &stdout, &stderr); got != exitOK {
		t.Fatalf("run(serve --print-config) = %d, want %d (stderr: %s)",
			got, exitOK, stderr.String())
	}
	if want := "BARQR_PORT=" + strconv.Itoa(port); !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("port %d is still held after --print-config: %v", port, err)
	}
	_ = ln.Close()
}
