package render

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"math"
	"strings"

	// Decoders for the raster formats a caller may hand us in a data URI. A
	// logo arrives as bytes, so the format list is a decode-side decision and
	// has nothing to do with what barqr can write.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Logo is an image placed over the centre of a code.
type Logo struct {
	// Image is the decoded logo. Nil means the caller only wants the geometry
	// reserved, which is what a vector writer that references an external
	// asset needs.
	Image image.Image
	// Scale is the logo's width as a fraction of the symbol's width, from
	// MinLogoScale to MaxLogoScale.
	Scale float64
	// Excavate clears the modules underneath the logo so it sits on the
	// background rather than on top of live data.
	Excavate bool
	// Padding is the clear space around the logo, in modules.
	Padding int
}

// Logo sizing limits.
const (
	// MinLogoScale is the smallest useful logo: below this it is a smudge.
	MinLogoScale = 0.05
	// MaxLogoScale caps the width at just over a third of the symbol. Even at
	// level H the error-correction budget is 30% of the codewords, and the
	// codewords a logo destroys are contiguous, which is the worst case for
	// Reed-Solomon block interleaving.
	MaxLogoScale = 0.35
	// DefaultLogoScale is a fifth of the width, comfortably inside level M.
	DefaultLogoScale = 0.2
	// MaxLogoPadding stops a small logo from excavating half the symbol
	// through its clear space alone.
	MaxLogoPadding = 8
)

// Decoding limits for ParseLogo. Both are decompression-bomb guards: the byte
// cap bounds what we hold, the pixel cap bounds what a decoder would allocate
// from it, and a 300-byte PNG can legitimately claim a 30000x30000 canvas.
const (
	maxLogoBytes  = 4 << 20
	maxLogoPixels = 4 << 20
)

// ParseLogo decodes a logo from a data URI.
//
// Only data: URIs are accepted. Fetching a logo over the network turns every
// render into an outbound request on behalf of an untrusted caller — a
// server-side request forgery primitive and a denial-of-service amplifier —
// so remote logos are a separate, disabled-by-default feature and are not
// reachable from here.
func ParseLogo(dataURI string) (image.Image, error) {
	raw := strings.TrimSpace(dataURI)

	const prefix = "data:"
	if !strings.HasPrefix(strings.ToLower(raw), prefix) {
		return nil, fmt.Errorf("%w: logo must be a data: URI, got a %q reference",
			ErrInvalidStyle, uriScheme(raw))
	}

	comma := strings.Index(raw, ",")
	if comma < 0 {
		return nil, fmt.Errorf("%w: logo data URI has no comma separating the payload",
			ErrInvalidStyle)
	}
	header := strings.ToLower(raw[len(prefix):comma])
	if !strings.Contains(header, ";base64") {
		return nil, fmt.Errorf("%w: logo data URI must be base64-encoded", ErrInvalidStyle)
	}
	if media, _, _ := strings.Cut(header, ";"); media != "" && !strings.HasPrefix(media, "image/") {
		return nil, fmt.Errorf("%w: logo media type is %q, expected an image/* type",
			ErrInvalidStyle, media)
	}

	payload := raw[comma+1:]
	// Check the encoded length before decoding: base64 expands by 4/3, so the
	// decoded size is known in advance and an oversized payload never gets
	// allocated.
	if len(payload)/4*3 > maxLogoBytes {
		return nil, fmt.Errorf("%w: logo is larger than %d bytes decoded",
			ErrInvalidStyle, maxLogoBytes)
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(payload)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: logo payload is not valid base64", ErrInvalidStyle)
	}

	// DecodeConfig reads only the header, so the dimensions are checked before
	// any pixel buffer is allocated.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: logo could not be decoded; expected png, jpeg, or gif",
			ErrInvalidStyle)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("%w: logo has no pixels", ErrInvalidStyle)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxLogoPixels {
		return nil, fmt.Errorf("%w: logo is %dx%d pixels, the limit is %d in total",
			ErrInvalidStyle, cfg.Width, cfg.Height, maxLogoPixels)
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: logo could not be decoded; expected png, jpeg, or gif",
			ErrInvalidStyle)
	}
	return img, nil
}

// uriScheme names the scheme of a URI for an error message, without echoing
// the URI itself back to the caller or into a log.
func uriScheme(s string) string {
	if i := strings.Index(s, ":"); i > 0 {
		return strings.ToLower(s[:i])
	}
	return "relative"
}

// validate checks the sizing a caller supplied.
func (l *Logo) validate() error {
	if l == nil {
		return nil
	}
	if l.Scale != 0 && (l.Scale < MinLogoScale || l.Scale > MaxLogoScale) {
		return fmt.Errorf("%w: logo scale is %g, expected between %g and %g",
			ErrInvalidStyle, l.Scale, MinLogoScale, MaxLogoScale)
	}
	if l.Padding < 0 || l.Padding > MaxLogoPadding {
		return fmt.Errorf("%w: logo padding is %d modules, expected 0 to %d",
			ErrInvalidStyle, l.Padding, MaxLogoPadding)
	}
	return nil
}

// LogoRect returns the logo's area in module coordinates, and false when the
// style carries no logo.
//
// The rectangle is centred on the symbol itself, not on the canvas: the quiet
// zone and any frame are margins, and a logo nudged off-centre by them would
// eat asymmetrically into the data.
func (c *Canvas) LogoRect() (image.Rectangle, bool) {
	l := c.Style.Logo
	if l == nil {
		return image.Rectangle{}, false
	}
	sym := c.SymbolRect()
	if sym.Empty() {
		return image.Rectangle{}, false
	}

	scale := l.Scale
	if scale <= 0 {
		scale = DefaultLogoScale
	}

	w := max(1, int(math.Round(float64(sym.Dx())*scale)))
	h := w
	// A non-square logo keeps its aspect ratio; stretching a wordmark to a
	// square is never what the caller meant.
	if l.Image != nil {
		b := l.Image.Bounds()
		if b.Dx() > 0 && b.Dy() > 0 {
			h = max(1, int(math.Round(float64(w)*float64(b.Dy())/float64(b.Dx()))))
		}
	}
	w, h = min(w, sym.Dx()), min(h, sym.Dy())

	x0 := sym.Min.X + (sym.Dx()-w)/2
	y0 := sym.Min.Y + (sym.Dy()-h)/2
	return image.Rect(x0, y0, x0+w, y0+h), true
}

// excavateRect is the area whose modules are cleared: the logo plus its clear
// space, confined to the symbol.
func (c *Canvas) excavateRect() (image.Rectangle, bool) {
	r, ok := c.LogoRect()
	if !ok {
		return image.Rectangle{}, false
	}
	pad := c.Style.Logo.Padding
	return r.Inset(-pad).Intersect(c.SymbolRect()), true
}

// excavate clears the modules under the logo.
//
// Finder-pattern modules are never cleared. A logo large enough to reach one
// would take away the landmark a decoder uses to find and orient the symbol,
// and unlike data modules that loss is not recoverable from error correction
// at any level — so it is refused silently here and reported loudly by
// Scannability.
func (c *Canvas) excavate() {
	r, ok := c.excavateRect()
	if !ok || r.Empty() {
		return
	}
	// r is already intersected with the symbol, which is itself inside the
	// canvas, so no bounds check is needed on the grid index here.
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if c.Role(x, y) != RoleData {
				continue
			}
			c.grid[y*c.Cols+x] = false
		}
	}
}

// logoCoverage is the fraction of the symbol's area the excavated region takes
// up, or zero when there is no logo.
func (c *Canvas) logoCoverage() float64 {
	r, ok := c.excavateRect()
	sym := c.SymbolRect()
	if !ok || sym.Empty() {
		return 0
	}
	return float64(r.Dx()*r.Dy()) / float64(sym.Dx()*sym.Dy())
}
