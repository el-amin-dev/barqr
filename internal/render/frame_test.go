package render

import (
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/el-amin-dev/barqr/internal/encoder"
)

// qrMatrix builds a synthetic square matrix that the renderer treats as a QR
// symbol: solid finder patterns in three corners and a checkerboard elsewhere,
// which gives every test a predictable dark-module count.
func qrMatrix(t *testing.T, size int) encoder.Matrix {
	t.Helper()

	m := encoder.NewMatrix(size, size)
	m.Symbology = "qr"
	m.Kind = encoder.Kind2D
	m.QuietZone = 4

	for y := range size {
		for x := range size {
			m.Set(x, y, (x+y)%2 == 0)
		}
	}
	for _, c := range [][2]int{{0, 0}, {size - eyeSize, 0}, {0, size - eyeSize}} {
		for dy := range eyeSize {
			for dx := range eyeSize {
				m.Set(c[0]+dx, c[1]+dy, true)
			}
		}
	}
	return m
}

// renderQR renders the synthetic matrix through the standard renderer.
func renderQR(t *testing.T, size int, s Style) Canvas {
	t.Helper()

	c, err := (standard{}).Render(qrMatrix(t, size), s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return c
}

func TestFrameExpandsTheCanvasWithoutShrinkingTheQuietZone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		frame    *Frame
		caption  string
		wantPad  int
		wantBand int
	}{
		{"no frame", nil, "", 0, 0},
		{"explicit none", &Frame{Kind: FrameNone}, "", 0, 0},
		{"border", &Frame{Kind: FrameBorder, Width: 3}, "", 3, 0},
		{"default width", &Frame{Kind: FrameRounded}, "", DefaultFrameWidth, 0},
		{"caption only", nil, "SCAN ME", 0, CaptionBandModules},
		{"banner with caption", &Frame{Kind: FrameBanner, Width: 2, Caption: "HELLO"}, "",
			2, CaptionBandModules},
		{"bubble reserves a tail", &Frame{Kind: FrameBubble, Width: 1}, "HI",
			1, CaptionBandModules + BubbleTailModules},
	}

	const size = 25

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := DefaultStyle()
			s.QuietZone = 4
			s.Frame = tt.frame
			s.Caption = tt.caption

			c := renderQR(t, size, s)

			wantCols := size + 2*4 + 2*tt.wantPad
			wantRows := size + 2*4 + 2*tt.wantPad + tt.wantBand
			if c.Cols != wantCols || c.Rows != wantRows {
				t.Errorf("canvas = %dx%d, want %dx%d", c.Cols, c.Rows, wantCols, wantRows)
			}
			// The frame must be added outside the quiet zone, never carved out
			// of it: the decoder needs that clear margin to find the symbol.
			if c.QuietZone != 4 {
				t.Errorf("QuietZone = %d, want the resolved 4 to be untouched", c.QuietZone)
			}
			sym := c.SymbolRect()
			if sym.Dx() != size || sym.Dy() != size {
				t.Errorf("SymbolRect = %v, want a %dx%d symbol", sym, size, size)
			}
			if got := sym.Min.X - tt.wantPad; got != 4 {
				t.Errorf("clear margin left of the symbol = %d modules, want 4", got)
			}
			if got := (c.Rows - tt.wantBand - tt.wantPad) - sym.Max.Y; got != 4 {
				t.Errorf("clear margin below the symbol = %d modules, want 4", got)
			}
		})
	}
}

func TestFrameRectCoversTheWholeCanvas(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.QuietZone = 4
	s.Frame = &Frame{Kind: FrameBorder, Width: 2}

	c := renderQR(t, 21, s)
	r, ok := c.FrameRect()
	if !ok {
		t.Fatal("FrameRect reported no frame")
	}
	if want := image.Rect(0, 0, c.Cols, c.Rows); r != want {
		t.Errorf("FrameRect = %v, want %v", r, want)
	}

	plain := renderQR(t, 21, DefaultStyle())
	if _, ok := plain.FrameRect(); ok {
		t.Error("FrameRect reported a frame on a plain style")
	}
}

func TestCaptionRectSitsBelowTheQuietZoneAndInsideTheFrame(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.QuietZone = 4
	s.Frame = &Frame{Kind: FrameBorder, Width: 3}
	s.Caption = "SCAN ME"

	c := renderQR(t, 21, s)
	r, ok := c.CaptionRect()
	if !ok {
		t.Fatal("CaptionRect reported no caption")
	}
	if r.Dy() != CaptionBandModules {
		t.Errorf("caption band height = %d, want %d", r.Dy(), CaptionBandModules)
	}
	if r.Min.X != 3 || r.Max.X != c.Cols-3 {
		t.Errorf("CaptionRect = %v, want it inset by the frame width", r)
	}
	if r.Max.Y != c.Rows-3 {
		t.Errorf("CaptionRect bottom = %d, want it above the frame's bottom edge", r.Max.Y)
	}
	if sym := c.SymbolRect(); r.Min.Y < sym.Max.Y+c.QuietZone {
		t.Errorf("CaptionRect %v starts inside the quiet zone below %v", r, sym)
	}
	if r.Overlaps(c.SymbolRect()) {
		t.Errorf("CaptionRect %v overlaps the symbol %v", r, c.SymbolRect())
	}

	plain := renderQR(t, 21, DefaultStyle())
	if _, ok := plain.CaptionRect(); ok {
		t.Error("CaptionRect reported a band on a style with no caption")
	}
}

func TestCaptionPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		style Style
		want  string
	}{
		{"neither", Style{}, ""},
		{"style only", Style{Caption: "top level"}, "top level"},
		{"frame only", Style{Frame: &Frame{Kind: FrameBanner, Caption: "framed"}}, "framed"},
		{
			name: "the frame wins",
			style: Style{
				Caption: "top level",
				Frame:   &Frame{Kind: FrameBanner, Caption: "framed"},
			},
			want: "framed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := Canvas{Style: tt.style}
			if got := c.Caption(); got != tt.want {
				t.Errorf("Caption() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFrameValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		frame   *Frame
		wantErr bool
	}{
		{"nil", nil, false},
		{"empty kind", &Frame{}, false},
		{"border", &Frame{Kind: FrameBorder, Width: 2}, false},
		{"rounded", &Frame{Kind: FrameRounded}, false},
		{"banner", &Frame{Kind: FrameBanner}, false},
		{"bubble", &Frame{Kind: FrameBubble}, false},
		{"unknown kind", &Frame{Kind: "sparkles"}, true},
		{"negative width", &Frame{Kind: FrameBorder, Width: -1}, true},
		{"absurd width", &Frame{Kind: FrameBorder, Width: MaxFrameWidth + 1}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.frame.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidStyle) {
				t.Errorf("error %v does not wrap ErrInvalidStyle", err)
			}
		})
	}
}

func TestRenderRejectsAnInvalidFrame(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.Frame = &Frame{Kind: "sparkles"}
	if _, err := (standard{}).Render(qrMatrix(t, 21), s); !errors.Is(err, ErrInvalidStyle) {
		t.Fatalf("Render error = %v, want ErrInvalidStyle", err)
	}
}

func TestRenderRejectsAnInvalidGradient(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.Gradient = &Gradient{Kind: "conic", Stops: make([]Stop, 2)}
	if _, err := (standard{}).Render(qrMatrix(t, 21), s); !errors.Is(err, ErrInvalidStyle) {
		t.Fatalf("Render error = %v, want ErrInvalidStyle", err)
	}
}

func TestFramedCanvasStillMarksItsFinderPatterns(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.QuietZone = 4
	s.Frame = &Frame{Kind: FrameBorder, Width: 3}
	s.Caption = "SCAN ME"

	c := renderQR(t, 21, s)
	sym := c.SymbolRect()

	// The finder roles must move with the symbol, or a writer would paint eye
	// shapes over data as soon as a frame was added.
	if got := c.Role(sym.Min.X, sym.Min.Y); got != RoleEyeFrame {
		t.Errorf("role at the symbol's top-left = %v, want RoleEyeFrame", got)
	}
	if got := c.Role(sym.Min.X+ballOffset, sym.Min.Y+ballOffset); got != RoleEyeBall {
		t.Errorf("role at the finder centre = %v, want RoleEyeBall", got)
	}
	if got := c.Role(0, 0); got != RoleData {
		t.Errorf("role in the frame = %v, want RoleData", got)
	}
}

func TestGradientColoursDataModulesOnly(t *testing.T) {
	t.Parallel()

	s := DefaultStyle()
	s.QuietZone = 4
	eye := color.NRGBA{R: 0xFF, A: 0xFF}
	s.EyeFG = &eye
	g, err := ParseGradient("linear(0deg,#000000,#0000ff)")
	if err != nil {
		t.Fatalf("ParseGradient: %v", err)
	}
	s.Gradient = g

	c := renderQR(t, 21, s)
	sym := c.SymbolRect()

	// Finder patterns keep the eye colour: a ramp running light across an eye
	// costs the scanner the landmark it locates the symbol with.
	if got := c.ColorAt(sym.Min.X, sym.Min.Y); got != eye {
		t.Errorf("finder colour = %v, want the eye colour %v", got, eye)
	}

	left := c.ColorAt(sym.Min.X+eyeSize+1, sym.Min.Y+10)
	right := c.ColorAt(sym.Max.X-1, sym.Min.Y+10)
	if left.B >= right.B {
		t.Errorf("gradient did not ramp across the symbol: %v then %v", left, right)
	}

	// With no gradient the flat foreground is used.
	plain := renderQR(t, 21, DefaultStyle())
	if got := plain.ColorAt(plain.QuietZone+10, plain.QuietZone+10); got != plain.Style.FG {
		t.Errorf("ColorAt = %v, want the flat foreground %v", got, plain.Style.FG)
	}
}
