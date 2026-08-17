package preset

// The built-in themes: ready-made appearances a caller can name instead of
// choosing shapes and three colours themselves.
//
// These are deliberately separate from the layout presets in builtin.go. Those
// answer "where is this code going" — a press, a terminal, a label — and set
// format, resolution and error correction. A theme answers "what should it look
// like" and touches nothing but shapes and colours, so the two compose: a theme
// can be applied on top of any output, and a request can override either.
//
// One rule runs through all of them, and it is the reason they are safe to ship
// as defaults: **the accent lives in eye_fg only.** A scanner locates the three
// finder patterns before it reads a single data module, so that is where colour
// can be spent for free. The data modules stay near-neutral against their
// background, which is what keeps the contrast ratio — and therefore the scan —
// reliable. A theme that tinted every module would look bolder on screen and
// fail on a phone camera in a shop.
//
// Every dark theme is light-on-dark, which barqr's scannability report flags as
// INVERTED. That is a warning rather than an error on purpose: plenty of
// scanners cope, and a caller who chooses a dark theme has chosen it. Run
// POST /v1/validate to see the verdict for a specific combination before
// committing it to print.
//
// Every accent here clears 4.5:1 against its own background, not merely the
// 3:1 floor the report treats as fatal. Several were deepened one shade from
// their design-system source for exactly that reason: the hue is what carries
// the identity, and a shade darker costs nothing visually while moving the
// code out of the range where a phone camera in poor light starts to guess.
// TestEveryThemeScans enforces both figures.
var themes = []Preset{
	// --- reference: faithful to the two design systems ----------------------
	{
		Name: "md3-light", Kind: KindTheme,
		Description: "Material Design 3, light — rounded modules, circular finder centres",
		Options:     theme("rounded", "rounded", "circle", "#1B1B1F", "#FFFBFE", "#6750A4"),
	},
	{
		Name: "md3-dark", Kind: KindTheme,
		Description: "Material Design 3, dark",
		Options:     theme("rounded", "rounded", "circle", "#E6E1E5", "#1C1B1F", "#D0BCFF"),
	},
	{
		Name: "gnome-light", Kind: KindTheme,
		Description: "GNOME Adwaita, light — square throughout with the Adwaita blue accent",
		Options:     theme("square", "square", "square", "#1A1A1A", "#FAFAFA", "#1C71D8"),
	},
	{
		Name: "gnome-dark", Kind: KindTheme,
		Description: "GNOME Adwaita, dark — Adwaita's own lighter blue, as the palette intends",
		Options:     theme("square", "square", "square", "#EEEEEC", "#1E1E1E", "#62A0EA"),
	},

	// --- dark: deep background, vivid accent --------------------------------
	{
		Name: "obsidian", Kind: KindTheme,
		Description: "Dotted modules on deep indigo, with a mauve accent",
		Options:     theme("dot", "circle", "circle", "#CBA6F7", "#1E1E2E", "#F5C2E7"),
	},
	{
		Name: "slate", Kind: KindTheme,
		Description: "Rounded modules on navy, with a sky accent",
		Options:     theme("rounded", "rounded", "rounded", "#CBD5E1", "#0F172A", "#38BDF8"),
	},
	{
		Name: "midnight-forest", Kind: KindTheme,
		Description: "Dotted modules on deep green, with leaf-shaped finders",
		Options:     theme("dot", "leaf", "circle", "#86EFAC", "#052E16", "#4ADE80"),
	},
	{
		Name: "ember-dark", Kind: KindTheme,
		Description: "Square modules on near-black brown, with shield finders",
		Options:     theme("square", "shield", "square", "#FED7AA", "#1C0A00", "#FB923C"),
	},
	{
		Name: "arctic-night", Kind: KindTheme,
		Description: "Rounded modules on midnight blue, with circular finders",
		Options:     theme("rounded", "circle", "circle", "#BFDBFE", "#0C1A2E", "#60A5FA"),
	},
	{
		Name: "rose-dark", Kind: KindTheme,
		Description: "Dotted modules on deep wine, with a rose accent",
		Options:     theme("dot", "rounded", "circle", "#FECDD3", "#1C0008", "#FB7185"),
	},
	{
		Name: "dusk", Kind: KindTheme,
		Description: "Classy modules on warm charcoal, with an amber accent",
		Options:     theme("classy", "square", "square", "#E7E5E4", "#1C1917", "#D97706"),
	},
	{
		Name: "indigo-night", Kind: KindTheme,
		Description: "Rounded modules on deep indigo, with shield finders",
		Options:     theme("rounded", "shield", "rounded", "#C7D2FE", "#0C0A2E", "#818CF8"),
	},
	{
		Name: "teal-void", Kind: KindTheme,
		Description: "Dotted modules on deep teal, with leaf finders",
		Options:     theme("dot", "leaf", "circle", "#99F6E4", "#042F2E", "#2DD4BF"),
	},
	{
		Name: "violet-night", Kind: KindTheme,
		Description: "Dotted modules on deep violet, with a purple accent",
		Options:     theme("dot", "circle", "circle", "#E9D5FF", "#1A0030", "#A855F7"),
	},

	// --- light: off-white background, muted modules, accent finders ---------
	{
		Name: "chalk", Kind: KindTheme,
		Description: "Dotted modules on warm paper, with a terracotta accent",
		Options:     theme("dot", "circle", "circle", "#2D2D2D", "#F5F0E8", "#9A3412"),
	},
	{
		Name: "daybreak", Kind: KindTheme,
		Description: "Rounded modules on pale sky, with a cyan accent",
		Options:     theme("rounded", "rounded", "rounded", "#0F172A", "#F0F9FF", "#0369A1"),
	},
	{
		Name: "forest", Kind: KindTheme,
		Description: "Dotted modules on pale green, with leaf-shaped finders",
		Options:     theme("dot", "leaf", "circle", "#1A2E1A", "#F0FDF4", "#15803D"),
	},
	{
		Name: "ember", Kind: KindTheme,
		Description: "Square modules on warm white, with shield finders",
		Options:     theme("square", "shield", "square", "#1C0A00", "#FFF7ED", "#C2410C"),
	},
	{
		Name: "arctic", Kind: KindTheme,
		Description: "Rounded modules on pale blue, with circular finders",
		Options:     theme("rounded", "circle", "circle", "#1E3A5F", "#EFF6FF", "#1D4ED8"),
	},
	{
		Name: "rose", Kind: KindTheme,
		Description: "Dotted modules on blush, with a crimson accent",
		Options:     theme("dot", "rounded", "circle", "#4C0519", "#FFF1F2", "#BE123C"),
	},
	{
		Name: "sand", Kind: KindTheme,
		Description: "Classy modules on warm grey, with an ochre accent",
		Options:     theme("classy", "square", "square", "#292524", "#FAFAF9", "#854D0E"),
	},
	{
		Name: "indigo", Kind: KindTheme,
		Description: "Rounded modules on pale indigo, with shield finders",
		Options:     theme("rounded", "shield", "rounded", "#1E1B4B", "#EEF2FF", "#4338CA"),
	},
	{
		Name: "teal-mist", Kind: KindTheme,
		Description: "Dotted modules on pale teal, with leaf finders",
		Options:     theme("dot", "leaf", "circle", "#134E4A", "#F0FDFA", "#0F766E"),
	},
	{
		Name: "violet-dew", Kind: KindTheme,
		Description: "Dotted modules on pale violet, with a purple accent",
		Options:     theme("dot", "circle", "circle", "#3B0764", "#FAF5FF", "#7C3AED"),
	},
	{
		Name: "copper", Kind: KindTheme,
		Description: "Classy modules on cream, with shield finders and a copper accent",
		Options:     theme("classy", "shield", "square", "#1C0F00", "#FFFBF5", "#B45309"),
	},

	// --- monochrome: no accent, contrast only -------------------------------
	{
		Name: "graphite", Kind: KindTheme,
		Description: "Horizontally merged modules on near-black, no accent",
		Options:     theme("horizontal", "square", "square", "#D4D4D4", "#171717", "#A3A3A3"),
	},
	{
		Name: "paper", Kind: KindTheme,
		Description: "Vertically merged modules on off-white, no accent",
		Options:     theme("vertical", "square", "square", "#262626", "#F5F5F5", "#404040"),
	},
	{
		Name: "ink", Kind: KindTheme,
		Description: "Pure black on pure white — the most scannable code barqr draws",
		Options:     theme("square", "square", "square", "#0A0A0A", "#FFFFFF", "#0A0A0A"),
	},
	{
		Name: "ghost", Kind: KindTheme,
		Description: "Dotted white on black, no accent",
		Options:     theme("dot", "circle", "circle", "#F5F5F5", "#09090B", "#A1A1AA"),
	},

	// --- terminal -----------------------------------------------------------
	{
		Name: "neon-void", Kind: KindTheme,
		Description: "Electric cyan on black with a magenta accent — a CRT, essentially",
		Options:     theme("dot", "circle", "circle", "#00FFD1", "#0A0A0A", "#FF00AA"),
	},
}

// theme builds a theme's option map.
//
// Every theme sets exactly these six keys and nothing else — no format, no
// scale, no error correction — which is what lets one compose with any layout
// preset or any output the caller asks for.
func theme(module, eye, eyeBall, fg, bg, eyeFG string) map[string]any {
	return map[string]any{
		"style.module":   module,
		"style.eye":      eye,
		"style.eye_ball": eyeBall,
		"style.fg":       fg,
		"style.bg":       bg,
		"style.eye_fg":   eyeFG,
	}
}
