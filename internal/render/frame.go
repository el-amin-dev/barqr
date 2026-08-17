package render

import (
	"fmt"
	"image"
	"image/color"
)

// Frame decorates a code with a border and an optional caption band.
//
// Nothing here is drawn by the renderer. A frame is a stroke and a caption is
// text, and both belong to the writer that knows about pixels or about fonts;
// what the renderer owns is the geometry, so that a raster writer and a vector
// writer place them identically.
type Frame struct {
	// Kind selects the outline: none, border, rounded, banner, or bubble.
	Kind string
	// Color is the frame's colour.
	Color color.NRGBA
	// Width is the frame's thickness in modules.
	Width int
	// Caption is the text for the caption band. It overrides Style.Caption
	// when both are set.
	Caption string
	// CaptionColor is the caption's text colour.
	CaptionColor color.NRGBA
}

// Frame kinds.
const (
	// FrameNone reserves no space at all.
	FrameNone = "none"
	// FrameBorder is a plain rectangular outline.
	FrameBorder = "border"
	// FrameRounded is a rectangular outline with rounded corners.
	FrameRounded = "rounded"
	// FrameBanner is a solid bar behind the caption with an outline around
	// the code.
	FrameBanner = "banner"
	// FrameBubble is a rounded outline with a speech-bubble tail below the
	// caption.
	FrameBubble = "bubble"
)

// Frame sizing limits and defaults.
const (
	// DefaultFrameWidth is a frame two modules thick: visible at the sizes a
	// code is printed at without dominating it.
	DefaultFrameWidth = 2
	// MaxFrameWidth caps the border so a frame cannot outgrow the symbol it
	// surrounds.
	MaxFrameWidth = 16
	// CaptionBandModules is the height reserved for the caption, in modules.
	// Four modules leaves room for text about two modules tall with a module
	// of breathing space above and below, which is the proportion a printed
	// linear code uses for its human-readable line.
	CaptionBandModules = 4
	// BubbleTailModules is the extra depth a speech-bubble tail needs under
	// the caption band.
	BubbleTailModules = 2
)

// frameKinds is the set Validate accepts.
var frameKinds = map[string]bool{
	FrameNone:    true,
	FrameBorder:  true,
	FrameRounded: true,
	FrameBanner:  true,
	FrameBubble:  true,
}

// validate checks the frame's kind and thickness.
func (f *Frame) validate() error {
	if f == nil {
		return nil
	}
	if f.Kind != "" && !frameKinds[f.Kind] {
		return fmt.Errorf("%w: frame kind %q: expected none, border, rounded, banner, or bubble",
			ErrInvalidStyle, f.Kind)
	}
	if f.Width < 0 || f.Width > MaxFrameWidth {
		return fmt.Errorf("%w: frame width is %d modules, expected 0 to %d",
			ErrInvalidStyle, f.Width, MaxFrameWidth)
	}
	return nil
}

// drawn reports whether this frame occupies any space.
func (f *Frame) drawn() bool {
	return f != nil && f.Kind != "" && f.Kind != FrameNone
}

// pad is the space the frame reserves on each side, in modules.
func (f *Frame) pad() int {
	if !f.drawn() {
		return 0
	}
	if f.Width <= 0 {
		return DefaultFrameWidth
	}
	return f.Width
}

// frameLayout resolves how much the canvas must grow for a style's frame and
// caption: pad on every side, band below the code.
//
// The space is always added OUTSIDE the quiet zone. That is the whole point of
// computing it here rather than letting a writer overlay a border: a frame
// drawn on top of the quiet zone is one of the most common ways a pretty code
// stops scanning, because the decoder needs that clear margin to find the
// symbol's edge at all and a two-module border eats half of a QR code's four.
func frameLayout(s Style) (pad, band int) {
	pad = s.Frame.pad()
	if captionText(s) != "" {
		band = CaptionBandModules
		if s.Frame.drawn() && s.Frame.Kind == FrameBubble {
			band += BubbleTailModules
		}
	}
	return pad, band
}

// captionText resolves the caption from the two places it can be set. The
// frame's own caption wins, because a caller who filled in a Frame is being
// more specific than one who set the top-level field.
func captionText(s Style) string {
	if s.Frame != nil && s.Frame.Caption != "" {
		return s.Frame.Caption
	}
	return s.Caption
}

// Caption returns the caption text a writer should draw, or empty when there
// is none.
func (c *Canvas) Caption() string { return captionText(c.Style) }

// SymbolRect is the code itself in module coordinates: quiet zone, frame and
// caption band all excluded.
//
// Writers that need to place something relative to the data — a logo, an
// overlay, a gradient — must use this rather than the canvas bounds, which
// drift as soon as a frame is added.
func (c *Canvas) SymbolRect() image.Rectangle {
	inset := c.pad + c.QuietZone
	return image.Rect(inset, inset, c.Cols-inset, c.Rows-c.band-inset)
}

// FrameRect returns the frame's outer bounds in module coordinates, and false
// when the style has no frame.
//
// The rectangle is the full canvas: the frame is a stroke of Frame.Width
// modules drawn inwards from these edges, so a writer strokes the outline and
// never has to work out where the code sits inside it.
func (c *Canvas) FrameRect() (image.Rectangle, bool) {
	if !c.Style.Frame.drawn() {
		return image.Rectangle{}, false
	}
	return image.Rect(0, 0, c.Cols, c.Rows), true
}

// CaptionRect returns the band reserved for the caption in module
// coordinates, and false when there is no caption.
//
// The band sits between the bottom of the quiet zone and the bottom of the
// frame, inset by the frame's thickness on the left and right. It never
// overlaps the quiet zone.
func (c *Canvas) CaptionRect() (image.Rectangle, bool) {
	if c.band <= 0 {
		return image.Rectangle{}, false
	}
	top := c.Rows - c.pad - c.band
	return image.Rect(c.pad, top, c.Cols-c.pad, top+c.band), true
}
