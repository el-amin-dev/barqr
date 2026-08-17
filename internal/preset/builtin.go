package preset

// The built-in presets.
//
// Each one exists because a real caller would otherwise send the same handful
// of options on every request, and getting one of them wrong produces a code
// that scans on a phone screen and fails on the device that matters. The
// option keys are the request layer's dot-notation paths; the shape and format
// names are registry names, so a preset naming a shape that this build does
// not carry fails at request time with the registry's own error rather than
// silently drawing squares.
//
// Values are the JSON types the request layer expects after decoding: strings
// for names and colours, float64 for numbers, bool for flags. They are written
// here as untyped constants and land as int/float64/bool/string in the map,
// which the request layer's reflective setter handles for each field's kind.
var builtins = []Preset{
	{
		Name: "default",
		Description: "PNG at eight pixels per module with the specification's " +
			"error correction — the shape of every other preset's starting point",
		Options: map[string]any{
			"output.format": "png",
			"output.scale":  8,
			"encode.ecc":    "M",
			"style.module":  "square",
			"style.fg":      "#000000",
			"style.bg":      "#ffffff",
		},
	},
	{
		Name: "print",
		Description: "300 dpi, sized in millimetres, highest error correction and a " +
			"generous quiet zone — for artwork that goes to a press",
		Options: map[string]any{
			"output.format": "png",
			"output.dpi":    300,
			"output.unit":   "mm",
			"output.size":   30.0,
			// H tolerates ink spread, misregistration and the scuffing a printed
			// code picks up between the press and the scanner. It costs capacity,
			// which a printed code has to spare.
			"encode.ecc": "H",
			// The specification's minimum is four modules. Print gets six: a
			// trimmed edge that eats two modules of margin is a common failure
			// and cheap to design out.
			"encode.quiet_zone": 6,
			"style.fg":          "#000000",
			"style.bg":          "#ffffff",
		},
	},
	{
		Name:        "terminal",
		Description: "ANSI half-block output sized for an 80-column terminal",
		Options: map[string]any{
			"output.format": "ansi",
			// One cell per module: the ANSI writer already packs two module rows
			// into one text row, and any scale above 1 overflows 80 columns for
			// all but the smallest symbols.
			"output.scale": 1,
			// Two modules of margin rather than four. A terminal's own padding
			// supplies the rest, and eight wasted columns matter at this size.
			"encode.quiet_zone": 2,
			"encode.ecc":        "M",
		},
	},
	{
		Name: "web",
		Description: "small SVG for inline use in a page, where the vector scales " +
			"and the bytes are the cost",
		Options: map[string]any{
			"output.format": "svg",
			"output.size":   128.0,
			"output.unit":   "px",
			// L keeps the symbol version — and so the path count — as low as the
			// payload allows. A code on a screen is not being scuffed.
			"encode.ecc":   "L",
			"style.module": "square",
		},
	},
	{
		Name: "ticket",
		Description: "boarding-pass and event-ticket stock: highest error correction " +
			"at thermal-printer resolution; set style.caption to the reference",
		Options: map[string]any{
			"output.format": "png",
			// 203 dpi is the near-universal resolution of direct-thermal ticket
			// and receipt printers; rendering at anything else makes the printer
			// resample and blurs the module edges.
			"output.dpi":  203,
			"output.unit": "mm",
			"output.size": 25.0,
			// A ticket is folded, pocketed and handed over damp. H is the only
			// level that survives that reliably.
			"encode.ecc":        "H",
			"encode.quiet_zone": 4,
			// The caption *text* deliberately is not here: it is per-ticket data,
			// not style, so the caller supplies style.caption and this preset
			// supplies everything around it.
			"style.fg": "#000000",
			"style.bg": "#ffffff",
		},
	},
	{
		Name: "label",
		Description: "linear barcode on adhesive label stock: tall bars, " +
			"human-readable line on, 1D quiet zone",
		Options: map[string]any{
			"output.format": "png",
			"output.dpi":    300,
			"output.unit":   "mm",
			"output.size":   50.0,
			// The human-readable line is what a person falls back to when the
			// scanner will not read, which on a warehouse label is the whole
			// point of printing the digits.
			"style.hri": true,
			// Linear symbologies are read by bar edges, so height buys tolerance
			// to a skewed scan the way error correction does for a 2D code.
			"style.bar_height": 40,
			// 1D symbologies specify a light margin of ten narrow modules, not
			// the four a 2D code asks for.
			"encode.quiet_zone": 10,
			// Symbology is deliberately unset: the endpoint or the caller picks
			// it, and pinning one here would break /v1/barcode/{symbology}.
			"style.fg": "#000000",
			"style.bg": "#ffffff",
		},
	},
	{
		Name: "dark",
		Description: "light modules on a dark background for dark-mode interfaces; " +
			"reversed polarity, so expect a scannability warning",
		Options: map[string]any{
			"output.format": "png",
			"output.scale":  8,
			"style.fg":      "#f5f5f5",
			"style.bg":      "#121212",
			// Reversed polarity is outside what the specifications describe and
			// older laser scanners will not read it at all. H is not a fix, but
			// it is the largest margin available to a code that starts at a
			// disadvantage.
			"encode.ecc": "H",
		},
	},
	{
		Name: "sticker",
		Description: "rounded modules with dot eyeballs for die-cut stickers and " +
			"merchandise, at the error correction that decoration costs",
		Options: map[string]any{
			"output.format":  "png",
			"output.scale":   10,
			"style.module":   "rounded",
			"style.eye":      "rounded",
			"style.eye_ball": "dot",
			// Rounded modules erode the corners a decoder samples, so the
			// decoration is paid for out of error-correction headroom rather
			// than out of the read rate.
			"encode.ecc":        "H",
			"encode.quiet_zone": 4,
		},
	},
}

// Builtin returns the presets compiled into the binary.
//
// The returned Set is a fresh copy, so a caller that loads a directory over it
// cannot mutate the built-ins seen by anything else.
func Builtin() *Set {
	presets := make([]Preset, 0, len(builtins))
	for _, p := range builtins {
		presets = append(presets, p.Clone())
	}
	return newSet(presets)
}
