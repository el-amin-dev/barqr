package writer

import (
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/render"
)

func TestUnicodeWriterIsRegisteredAsText(t *testing.T) {
	t.Parallel()

	w, err := Get(Unicode)
	if err != nil {
		t.Fatalf("Get(unicode): %v", err)
	}
	if w.MIME() != "text/plain; charset=utf-8" || w.Extension() != "txt" || w.Binary() {
		t.Errorf("mime=%q ext=%q binary=%v", w.MIME(), w.Extension(), w.Binary())
	}
}

func TestUnicodeWritePacksTwoRowsPerLine(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	out, err := unicodeWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	want := (c.Rows + 1) / 2
	if len(lines) != want {
		t.Fatalf("%d lines, want %d for %d rows", len(lines), want, c.Rows)
	}

	for i, line := range lines {
		cells := []rune(line)
		if len(cells) != c.Cols {
			t.Fatalf("line %d has %d cells, want %d", i, len(cells), c.Cols)
		}
		top, bottom := 2*i, 2*i+1
		for x, cell := range cells {
			// The last line of an odd-height canvas has no lower row; At
			// reports out-of-range coordinates as light, which is what the
			// expectation below relies on.
			var expect rune
			switch {
			case c.At(x, top) && c.At(x, bottom):
				expect = blockFull
			case c.At(x, top):
				expect = blockUpper
			case c.At(x, bottom):
				expect = blockLower
			default:
				expect = blockNone
			}
			if cell != expect {
				t.Fatalf("cell (%d,%d) = %q, want %q", x, i, cell, expect)
			}
		}
	}
}

func TestUnicodeWriteHandlesAnOddNumberOfRows(t *testing.T) {
	t.Parallel()

	c := rasterQR(t, render.DefaultStyle())
	if c.Rows%2 == 0 {
		t.Skipf("canvas has an even %d rows; the odd case is covered elsewhere", c.Rows)
	}

	out, err := unicodeWriter{}.Write(c, OutputOpts{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	last := []rune(lines[len(lines)-1])
	for x, cell := range last {
		if cell == blockFull || cell == blockLower {
			t.Fatalf("cell %d of the final line is %q: it claims a row that does not exist", x, cell)
		}
	}
}
