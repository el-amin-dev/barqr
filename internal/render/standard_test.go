package render_test

import (
	"errors"
	"image/color"
	"testing"

	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/render"
)

// qrMatrix encodes a payload with the QR encoder, for renderer tests.
func qrMatrix(t *testing.T, data string) encoder.Matrix {
	t.Helper()

	enc, err := encoder.Get(encoder.QR)
	if err != nil {
		t.Fatalf("encoder.Get(qr): %v", err)
	}
	m, err := enc.Encode(data, encoder.AutoEncodeOpts())
	if err != nil {
		t.Fatalf("Encode(%q): %v", data, err)
	}
	return m
}

// standard returns the registered standard renderer.
func standard(t *testing.T) render.Renderer {
	t.Helper()

	r, err := render.Get(render.StandardRenderer)
	if err != nil {
		t.Fatalf("render.Get(standard): %v", err)
	}
	return r
}

func TestRenderAppliesQuietZone(t *testing.T) {
	t.Parallel()

	m := qrMatrix(t, "quiet zone")
	c, err := standard(t).Render(m, render.DefaultStyle())
	if err != nil {
		t.Fatalf("Render() returned error: %v", err)
	}

	// The default style asks for the symbology's own margin, which for QR is
	// four modules on every side.
	if got, want := c.QuietZone, 4; got != want {
		t.Errorf("QuietZone = %d, want %d", got, want)
	}
	if got, want := c.Cols, m.Cols+8; got != want {
		t.Errorf("Cols = %d, want %d (matrix plus both margins)", got, want)
	}
	if got, want := c.Rows, m.Rows+8; got != want {
		t.Errorf("Rows = %d, want %d", got, want)
	}

	// Every module of the margin itself must be light, or it is not a quiet
	// zone.
	for x := range c.Cols {
		for y := range c.Rows {
			inMargin := x < c.QuietZone || y < c.QuietZone ||
				x >= c.Cols-c.QuietZone || y >= c.Rows-c.QuietZone
			if inMargin && c.At(x, y) {
				t.Fatalf("module (%d, %d) inside the quiet zone is dark", x, y)
			}
		}
	}
	if got, want := c.Dark(), m.Dark(); got != want {
		t.Errorf("Dark() = %d, want %d; the quiet zone must not change the module count",
			got, want)
	}
}

func TestRenderQuietZoneOverride(t *testing.T) {
	t.Parallel()

	m := qrMatrix(t, "override")
	st := render.DefaultStyle()
	st.QuietZone = 0

	c, err := standard(t).Render(m, st)
	if err != nil {
		t.Fatalf("Render() returned error: %v", err)
	}
	if got, want := c.Cols, m.Cols; got != want {
		t.Errorf("Cols = %d, want %d with the quiet zone suppressed", got, want)
	}
}

func TestRenderMarksFinderPatterns(t *testing.T) {
	t.Parallel()

	m := qrMatrix(t, "finders")
	c, err := standard(t).Render(m, render.DefaultStyle())
	if err != nil {
		t.Fatalf("Render() returned error: %v", err)
	}

	q := c.QuietZone
	// Top-left finder: the outer ring is frame, the 3x3 centre is the ball.
	if got, want := c.Role(q, q), render.RoleEyeFrame; got != want {
		t.Errorf("Role at the finder's outer corner = %v, want %v", got, want)
	}
	if got, want := c.Role(q+3, q+3), render.RoleEyeBall; got != want {
		t.Errorf("Role at the finder's centre = %v, want %v", got, want)
	}
	// A module well inside the data area belongs to no finder.
	if got, want := c.Role(q+m.Cols/2, q+m.Rows/2), render.RoleData; got != want {
		t.Errorf("Role in the data area = %v, want %v", got, want)
	}

	// All three finders are marked, and the fourth corner is not.
	corners := [][2]int{{q, q}, {q + m.Cols - 7, q}, {q, q + m.Rows - 7}}
	for _, p := range corners {
		if c.Role(p[0], p[1]) == render.RoleData {
			t.Errorf("finder at (%d, %d) is not marked", p[0], p[1])
		}
	}
	if c.Role(q+m.Cols-1, q+m.Rows-1) != render.RoleData {
		t.Error("the bottom-right corner is marked as a finder; QR has only three")
	}
}

func TestCanvasColorAt(t *testing.T) {
	t.Parallel()

	m := qrMatrix(t, "colours")
	st := render.DefaultStyle()
	eye := color.NRGBA{R: 0x20, G: 0x40, B: 0xC0, A: 0xFF}
	st.EyeFG = &eye

	c, err := standard(t).Render(m, st)
	if err != nil {
		t.Fatalf("Render() returned error: %v", err)
	}

	q := c.QuietZone
	if got := c.ColorAt(q, q); got != eye {
		t.Errorf("ColorAt(finder) = %+v, want the eye colour %+v", got, eye)
	}
	if got := c.ColorAt(q+m.Cols/2, q+m.Rows/2); got != st.FG {
		t.Errorf("ColorAt(data) = %+v, want the foreground %+v", got, st.FG)
	}
}

func TestRenderRejections(t *testing.T) {
	t.Parallel()

	m := qrMatrix(t, "rejections")
	r := standard(t)

	tests := []struct {
		name    string
		style   func(render.Style) render.Style
		matrix  encoder.Matrix
		wantErr error
	}{
		{
			name:    "unknown module shape",
			style:   func(s render.Style) render.Style { s.Module = "hexagon"; return s },
			matrix:  m,
			wantErr: render.ErrUnknownShape,
		},
		{
			name:    "unknown eye shape",
			style:   func(s render.Style) render.Style { s.Eye = "starburst"; return s },
			matrix:  m,
			wantErr: render.ErrUnknownShape,
		},
		{
			name:    "negative quiet zone",
			style:   func(s render.Style) render.Style { s.QuietZone = -1; return s },
			matrix:  encoder.Matrix{Cols: 2, Rows: 2, QuietZone: -1},
			wantErr: render.ErrInvalidStyle,
		},
		{
			name:    "empty matrix",
			style:   func(s render.Style) render.Style { return s },
			matrix:  encoder.Matrix{},
			wantErr: render.ErrInvalidStyle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := r.Render(tt.matrix, tt.style(render.DefaultStyle())); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Render() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestRenderExtrudesLinearCodes checks the 1D path: a one-row matrix becomes a
// canvas tall enough to scan, with every row identical.
func TestRenderExtrudesLinearCodes(t *testing.T) {
	t.Parallel()

	m := encoder.NewMatrix(40, 1)
	m.Symbology, m.Kind, m.QuietZone, m.HRI = "code128", encoder.Kind1D, 10, "12345"
	for x := 0; x < 40; x += 2 {
		m.Set(x, 0, true)
	}

	st := render.DefaultStyle()
	st.BarHeight = 30

	c, err := standard(t).Render(m, st)
	if err != nil {
		t.Fatalf("Render() returned error: %v", err)
	}

	if got, want := c.Rows, 30+2*10; got != want {
		t.Errorf("Rows = %d, want %d (bar height plus both margins)", got, want)
	}
	if got, want := c.HRI, "12345"; got != want {
		t.Errorf("HRI = %q, want %q", got, want)
	}
	// Every bar row must be identical, or the bars are not straight.
	q := c.QuietZone
	for y := q + 1; y < c.Rows-q; y++ {
		for x := range c.Cols {
			if c.At(x, y) != c.At(x, q) {
				t.Fatalf("row %d differs from the first bar row at column %d", y, x)
			}
		}
	}
	// A 1D canvas has no finder patterns to style.
	if c.Role(q, q) != render.RoleData {
		t.Error("a linear code should have no eye roles")
	}
}

func TestRenderSuppressesHRI(t *testing.T) {
	t.Parallel()

	m := encoder.NewMatrix(10, 1)
	m.Symbology, m.Kind, m.QuietZone, m.HRI = "code128", encoder.Kind1D, 10, "12345"

	st := render.DefaultStyle()
	st.HRI = false

	c, err := standard(t).Render(m, st)
	if err != nil {
		t.Fatalf("Render() returned error: %v", err)
	}
	if c.HRI != "" {
		t.Errorf("HRI = %q, want empty when style.hri is false", c.HRI)
	}
}

func TestRegistryLookups(t *testing.T) {
	t.Parallel()

	if _, err := render.Get("nonexistent"); !errors.Is(err, render.ErrUnknownRenderer) {
		t.Errorf("Get() error = %v, want %v", err, render.ErrUnknownRenderer)
	}
	if _, err := render.ModuleShape("nonexistent"); !errors.Is(err, render.ErrUnknownShape) {
		t.Errorf("ModuleShape() error = %v, want %v", err, render.ErrUnknownShape)
	}
	if _, err := render.EyeShape("nonexistent"); !errors.Is(err, render.ErrUnknownShape) {
		t.Errorf("EyeShape() error = %v, want %v", err, render.ErrUnknownShape)
	}

	// The square shapes are the specified appearance and must always exist:
	// they are the fallback every other style is measured against.
	if _, err := render.ModuleShape(render.ShapeSquare); err != nil {
		t.Errorf("the square module shape is not registered: %v", err)
	}
	if _, err := render.EyeShape(render.ShapeSquare); err != nil {
		t.Errorf("the square eye shape is not registered: %v", err)
	}
	for _, names := range [][]string{render.ModuleShapes(), render.EyeShapes()} {
		if len(names) == 0 {
			t.Error("a shape registry is empty")
		}
	}
}

func BenchmarkRenderQR(b *testing.B) {
	enc, _ := encoder.Get(encoder.QR)
	m, _ := enc.Encode("https://example.com/benchmark", encoder.AutoEncodeOpts())
	r, _ := render.Get(render.StandardRenderer)
	st := render.DefaultStyle()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.Render(m, st); err != nil {
			b.Fatal(err)
		}
	}
}
