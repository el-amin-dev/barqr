package writer

import (
	"fmt"
	"maps"
	"slices"

	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/el-amin-dev/barqr/internal/render"
)

// maxGlyphRows is the tallest cell a face may use. It bounds glyphBits so a
// glyph stays a value type: a face table cannot be mutated through a glyph
// somebody was handed.
const maxGlyphRows = 16

// glyphBits is one glyph cell — up to maxGlyphRows rows of up to eight
// columns, most significant bit leftmost. Rows past the face's own height are
// zero and never drawn.
type glyphBits [maxGlyphRows]uint8

// bitmapFace is a fixed-cell bitmap type family.
//
// Everything that measures or draws text goes through a face, so the cell size
// and the gap cannot drift apart between measuring and drawing — the hazard
// that used to exist when the advance was spelled out at three call sites.
type bitmapFace struct {
	name   string
	cols   int // cell width in font pixels, at most 8
	rows   int // cell height in font pixels, at most maxGlyphRows
	gap    int // blank columns between adjacent cells, in font pixels
	glyphs map[rune]glyphBits
}

// glyph returns the cell for a character, folding case and falling back to a
// blank. Unknown characters advance without drawing rather than being dropped,
// so the text stays aligned under the bars it describes.
func (f *bitmapFace) glyph(r rune) glyphBits {
	if r >= 'a' && r <= 'z' {
		r -= 'a' - 'A'
	}
	if g, ok := f.glyphs[r]; ok {
		return g
	}
	return f.glyphs[' ']
}

// advance is the distance from one glyph's left edge to the next.
func (f *bitmapFace) advance(pixel int) int { return (f.cols + f.gap) * pixel }

// textWidth is the width of n glyphs, with the face's gap between them.
func (f *bitmapFace) textWidth(n, pixel int) int {
	if n == 0 {
		return 0
	}
	return n*f.advance(pixel) - f.gap*pixel
}

// maxGlyphs is how many glyphs fit in w pixels at this font-pixel size.
// textWidth(n, pixel) is pixel*((cols+gap)*n - gap), so it inverts directly.
func (f *bitmapFace) maxGlyphs(w, pixel int) int {
	return (w/pixel + f.gap) / (f.cols + f.gap)
}

// hriAlphabet is what a linear symbology can put in its human-readable line —
// digits, capitals and the Code 39 punctuation set — because a barcode's HRI
// is by definition a subset of what it encodes. Every face must cover it.
func hriAlphabet() []rune { return slices.Sorted(maps.Keys(font5x7)) }

// faceMono is the hand-drawn 5x7 face: the default, and the only one that
// stays legible when a code is printed small enough that a glyph is five
// pixels wide.
var faceMono = &bitmapFace{
	name:   render.HRIFontMono,
	cols:   fontCols,
	rows:   fontRows,
	gap:    fontGap,
	glyphs: monoGlyphs(),
}

func monoGlyphs() map[rune]glyphBits {
	out := make(map[rune]glyphBits, len(font5x7))
	for r, rows := range font5x7 {
		var g glyphBits
		copy(g[:], rows[:])
		out[r] = g
	}
	return out
}

// faceSans is X11's misc-fixed 7x13, taken from golang.org/x/image, which is
// already a dependency for image decoding. It is converted to this package's
// own representation once at init rather than drawn through a font.Drawer at
// render time, for two reasons: drawing stays one loop over bits, and the
// output keeps hard edges at any integer scale. An anti-aliased HRI line would
// defeat the point — it is read when the scan already failed.
var faceSans = faceFromBasic(render.HRIFontSans, basicfont.Face7x13, sansOverrides)

// sansOverrides redraws the glyphs where the borrowed face carries the same
// weakness the hand-drawn one did.
//
// This is not a matter of taste. Held to the same bar as faceMono, misc-fixed
// fails it: its 'O' and 'Q' differ by three pixels — a two-pixel tick inside
// an identical ring — and 'C'/'O', 'P'/'R' and 'F'/'P' are barely better. A
// general-purpose screen font has no reason to care, because prose gives the
// reader context; a barcode's human-readable line gives none, and is read at
// the moment the scan already failed.
//
// Rows are drawn rather than encoded so a glyph is edited by looking at it.
// Every row must be exactly the face's cell width; init panics otherwise,
// because a silently truncated glyph is how this class of bug returns.
var sansOverrides = map[rune][]string{
	// A tail that leaves the ring, instead of a tick inside it.
	'Q': {
		"......",
		"......",
		".####.",
		"#....#",
		"#....#",
		"#....#",
		"#....#",
		"#....#",
		"#....#",
		"#....#",
		".####.",
		"..####",
		"....##",
	},
	// Flat terminals open the aperture, so the ring is unmistakably broken.
	'C': {
		"......",
		"......",
		".#####",
		"#.....",
		"#.....",
		"#.....",
		"#.....",
		"#.....",
		"#.....",
		"#.....",
		".#####",
		"......",
		"......",
	},
	// A leg with weight, rather than a one-pixel diagonal.
	'R': {
		"......",
		"......",
		"#####.",
		"#....#",
		"#....#",
		"#....#",
		"#####.",
		"#.##..",
		"#..##.",
		"#...##",
		"#....#",
		"......",
		"......",
	},
	// A deeper bowl, which is what separates P from F as well as from R.
	'P': {
		"......",
		"......",
		"#####.",
		"#....#",
		"#....#",
		"#....#",
		"#....#",
		"#####.",
		"#.....",
		"#.....",
		"#.....",
		"......",
		"......",
	},
}

func faceFromBasic(name string, src *basicfont.Face, overrides map[rune][]string) *bitmapFace {
	f := &bitmapFace{
		name:   name,
		cols:   src.Width,
		rows:   src.Height,
		gap:    fontGap,
		glyphs: make(map[rune]glyphBits, len(font5x7)),
	}
	for _, r := range hriAlphabet() {
		dr, mask, maskp, _, ok := src.Glyph(fixed.P(0, src.Ascent), r)
		if !ok {
			continue
		}
		var g glyphBits
		for y := dr.Min.Y; y < dr.Max.Y; y++ {
			if y < 0 || y >= f.rows || y >= maxGlyphRows {
				continue
			}
			for x := dr.Min.X; x < dr.Max.X; x++ {
				if x < 0 || x >= f.cols || x >= 8 {
					continue
				}
				_, _, _, a := mask.At(maskp.X+x-dr.Min.X, maskp.Y+y-dr.Min.Y).RGBA()
				if a >= 0x8000 {
					g[y] |= 1 << uint(f.cols-1-x)
				}
			}
		}
		f.glyphs[r] = g
	}

	for r, rows := range overrides {
		f.glyphs[r] = drawGlyph(f, r, rows)
	}
	return f
}

// drawGlyph turns a hand-drawn cell into bits, panicking on a cell that is not
// exactly the face's size. This runs at init, so a mistake stops the process
// rather than shipping a clipped glyph nobody notices until it is printed.
func drawGlyph(f *bitmapFace, r rune, rows []string) glyphBits {
	if len(rows) != f.rows {
		panic(fmt.Sprintf("writer: %s override for %q has %d rows, want %d",
			f.name, r, len(rows), f.rows))
	}
	var g glyphBits
	for y, row := range rows {
		if len(row) != f.cols {
			panic(fmt.Sprintf("writer: %s override for %q row %d is %d wide, want %d",
				f.name, r, y, len(row), f.cols))
		}
		for x, c := range row {
			if c == '#' {
				g[y] |= 1 << uint(f.cols-1-x)
			}
		}
	}
	return g
}

// hriFaces is the raster half of the style.hri_font enum. It is keyed by the
// same names render.HRIFonts() advertises, and a test holds the two to each
// other: a family the API offers and the rasteriser cannot draw would be an
// option accepted for one output format and silently ignored for another.
var hriFaces = map[string]*bitmapFace{
	render.HRIFontMono: faceMono,
	render.HRIFontSans: faceSans,
}

// hriFace resolves a family name. An empty or unknown name is the default;
// unknown names are rejected before they reach here, at the HTTP edge and
// again in the renderer, so this is a fallback and not the validation.
func hriFace(name string) *bitmapFace {
	if f, ok := hriFaces[name]; ok {
		return f
	}
	return faceMono
}
