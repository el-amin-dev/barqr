package writer

import (
	"image/color"
	"regexp"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/render"
)

// ansiEscape matches a CSI sequence, so a test can look at the cells alone.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestANSIWriterIsRegisteredAsText(t *testing.T) {
	t.Parallel()

	w, err := Get(ANSI)
	if err != nil {
		t.Fatalf("Get(ansi): %v", err)
	}
	if w.MIME() != "text/plain; charset=utf-8" || w.Extension() != "txt" || w.Binary() {
		t.Errorf("mime=%q ext=%q binary=%v", w.MIME(), w.Extension(), w.Binary())
	}
}

func TestANSIWriteEndsEveryLineWithAReset(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	out, err := ansiWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != c.Rows {
		t.Fatalf("%d lines, want %d", len(lines), c.Rows)
	}
	for i, line := range lines {
		if !strings.HasSuffix(line, ansiReset) {
			// Without this the terminal keeps painting the last colour past
			// the code, which is both ugly and unscannable.
			t.Fatalf("line %d does not end with a reset", i)
		}
		if got := len(ansiEscape.ReplaceAllString(line, "")); got != 2*c.Cols {
			t.Fatalf("line %d has %d visible columns, want %d", i, got, 2*c.Cols)
		}
	}
}

func TestANSIWriteColoursModulesFromTheStyle(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	out, err := ansiWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	text := string(out)
	for _, want := range []string{"\x1b[48;2;0;0;0m", "\x1b[48;2;255;255;255m"} {
		if !strings.Contains(text, want) {
			t.Errorf("output does not contain %q", want)
		}
	}

	// The first line is entirely quiet zone, so it needs exactly one escape.
	first := strings.SplitN(text, "\n", 2)[0]
	if got := len(ansiEscape.FindAllString(first, -1)); got != 2 {
		t.Errorf("quiet-zone line carries %d escapes, want 2 (one colour, one reset)", got)
	}
}

func TestANSIWriteFallsBackToTheTerminalBackground(t *testing.T) {
	t.Parallel()

	s := render.DefaultStyle()
	s.BG = render.Transparent
	c := rasterQR(t, s)

	out, err := ansiWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(string(out), ansiDefaultBG) {
		t.Errorf("a transparent background did not emit %q", ansiDefaultBG)
	}
}

func TestOverWhiteCompositesStraightAlpha(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      color.NRGBA
		r, g, b uint8
	}{
		{"opaque passes through", color.NRGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xFF}, 0x12, 0x34, 0x56},
		{"transparent black is white", color.NRGBA{A: 0}, 0xFF, 0xFF, 0xFF},
		{"half-transparent black is mid grey", color.NRGBA{A: 0x80}, 0x7F, 0x7F, 0x7F},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, g, b := overWhite(tt.in)
			if r != tt.r || g != tt.g || b != tt.b {
				t.Errorf("overWhite(%v) = %d,%d,%d, want %d,%d,%d",
					tt.in, r, g, b, tt.r, tt.g, tt.b)
			}
		})
	}
}
