package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/render"
)

func TestASCIIWritersAreRegisteredUnderBothNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{ASCII, Text} {
		w, err := Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		if w.Name() != name {
			t.Errorf("Name() = %q, want %q", w.Name(), name)
		}
		if w.MIME() != "text/plain; charset=utf-8" || w.Extension() != "txt" || w.Binary() {
			t.Errorf("%s: mime=%q ext=%q binary=%v", name, w.MIME(), w.Extension(), w.Binary())
		}
	}
}

func TestASCIIWriteDrawsTwoColumnsPerModule(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	out, err := asciiWriter{format: ASCII}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	text := string(out)
	if !strings.HasSuffix(text, "\n") {
		t.Fatal("output does not end with a newline")
	}

	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != c.Rows {
		t.Fatalf("%d lines, want %d", len(lines), c.Rows)
	}

	for y, line := range lines {
		if len(line) != 2*c.Cols {
			t.Fatalf("line %d is %d characters, want %d", y, len(line), 2*c.Cols)
		}
		for x := range c.Cols {
			cell := line[2*x : 2*x+2]
			want := "  "
			if c.At(x, y) {
				want = "##"
			}
			if cell != want {
				t.Fatalf("module (%d,%d) = %q, want %q", x, y, cell, want)
			}
		}
	}
}

func TestTextAliasMatchesASCIIByteForByte(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	first, err := asciiWriter{format: ASCII}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	second, err := asciiWriter{format: Text}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if string(first) != string(second) {
		t.Error("the txt alias produced different bytes from ascii")
	}
}

func TestTextWritersRejectAnEmptyCanvas(t *testing.T) {
	t.Parallel()

	writers := []Writer{
		asciiWriter{format: ASCII},
		unicodeWriter{},
		ansiWriter{},
		jsonWriter{},
	}
	for _, w := range writers {
		t.Run(w.Name(), func(t *testing.T) {
			t.Parallel()

			if _, err := w.Write(render.Canvas{}, OutputOpts{}); !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidOutput)
			}
		})
	}
}
