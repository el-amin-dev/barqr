package httpapi

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Request is the one request shape barqr accepts, from all three transports.
//
// A query string, a JSON body, and a multipart form all decode into this same
// struct: the query uses dot notation (`style.module=dot`), JSON uses nesting,
// and multipart uses the query's flat keys. Keeping a single struct is what
// makes "every option reachable as a query param or a body field" true by
// construction rather than by discipline.
//
// Unset is distinguishable from zero throughout. Scalars use a sentinel — an
// empty string, or zero where zero is not a legal value — and options where
// zero *is* legal use a pointer or AutoInt.
type Request struct {
	// Type names a builder. When set, Payload is built into the encoded
	// string and Data is ignored.
	Type string `json:"type,omitempty"`
	// Payload is the builder's input. Query and multipart set its members
	// with `payload.<key>=<value>`.
	Payload map[string]any `json:"payload,omitempty"`
	// Data is the raw string to encode when no Type is given.
	Data string `json:"data,omitempty"`

	// Symbology names the encoder. Empty means the endpoint's default: "qr"
	// for /v1/qr, the path parameter for /v1/barcode/{symbology}.
	Symbology string `json:"symbology,omitempty"`

	Encode EncodeSection `json:"encode,omitzero"`
	Style  StyleSection  `json:"style,omitzero"`
	Output OutputSection `json:"output,omitzero"`
	Meta   MetaSection   `json:"meta,omitzero"`
}

// EncodeSection holds the symbology-level options.
type EncodeSection struct {
	// ECC is the error-correction level. Empty means the configured default.
	ECC string `json:"ecc,omitempty"`
	// Version pins the symbology version. Zero or "auto" selects it.
	Version AutoInt `json:"version,omitzero"`
	// Mask pins the data-mask pattern. Zero value or "auto" selects it.
	Mask AutoInt `json:"mask,omitzero"`
	// QuietZone overrides the margin in modules. Nil uses the symbology's own.
	QuietZone *int `json:"quiet_zone,omitempty"`
}

// StyleSection holds the appearance options.
type StyleSection struct {
	// Module, Eye, and EyeBall name registered shapes.
	Module  string `json:"module,omitempty"`
	Eye     string `json:"eye,omitempty"`
	EyeBall string `json:"eye_ball,omitempty"`
	// FG, BG, and EyeFG are colours: #rgb, #rrggbb, #rrggbbaa, or a name.
	FG    string `json:"fg,omitempty"`
	BG    string `json:"bg,omitempty"`
	EyeFG string `json:"eye_fg,omitempty"`
	// BarHeight is the height of a linear code in modules. Zero is automatic.
	BarHeight int `json:"bar_height,omitempty"`
	// HRI controls the human-readable text under a linear code. Nil means on.
	HRI *bool `json:"hri,omitempty"`
	// HRISize is the height of that text in modules. Zero means the default.
	HRISize float64 `json:"hri_size,omitempty"`
	// HRIFont is the type family for it: mono or sans. Empty means mono.
	HRIFont string `json:"hri_font,omitempty"`
	// Logo is a data URI or, when remote fetching is enabled, a URL.
	Logo string `json:"logo,omitempty"`
	// LogoScale is the logo's width as a fraction of the code, 0.05..0.35.
	LogoScale float64 `json:"logo_scale,omitempty"`
	// Excavate clears the modules behind the logo instead of drawing over
	// them, which reads better but costs error-correction headroom.
	Excavate *bool `json:"excavate,omitempty"`
	// LogoPadding is the clear space around the logo, in modules.
	LogoPadding *int `json:"logo_padding,omitempty"`
	// Caption is text drawn beneath the code.
	Caption string `json:"caption,omitempty"`
	// Frame names a frame style drawn around the code: border, rounded,
	// banner, or bubble.
	Frame string `json:"frame,omitempty"`
	// FrameColor and FrameWidth style that frame. An unset colour follows the
	// foreground, and an unset width takes the renderer's default.
	FrameColor string `json:"frame_color,omitempty"`
	FrameWidth *int   `json:"frame_width,omitempty"`
	// CaptionColor colours the caption text.
	CaptionColor string `json:"caption_color,omitempty"`
	// Gradient replaces the flat foreground on data modules, e.g.
	// "linear(45deg,#000,#00f)" or "radial(#000,#333)".
	Gradient string `json:"gradient,omitempty"`
}

// OutputSection holds the serialisation options.
type OutputSection struct {
	// Format names a registered writer. Empty means the configured default.
	Format string `json:"format,omitempty"`
	// Scale is pixels per module. Zero means derive from Size, else default.
	Scale int `json:"scale,omitempty"`
	// Size is the target width in Unit. Zero means derive from Scale.
	Size float64 `json:"size,omitempty"`
	// Unit interprets Size: px, mm, or in.
	Unit string `json:"unit,omitempty"`
	// DPI converts physical units to pixels. Zero means 300.
	DPI int `json:"dpi,omitempty"`
	// Quality is the JPEG and WebP quality, 1..100. Zero means 92.
	Quality int `json:"quality,omitempty"`
}

// MetaSection holds response-shaping options that do not affect the image.
type MetaSection struct {
	// Filename names the file in Content-Disposition.
	Filename string `json:"filename,omitempty"`
	// Attachment switches Content-Disposition from inline to attachment.
	Attachment bool `json:"attachment,omitempty"`
}

// AutoInt is an integer option that also accepts the literal "auto".
//
// The QR specification calls these "automatic" rather than "0", and `0` is a
// legal value for a mask pattern, so a plain int cannot express "not set". The
// zero value is automatic, which is what a caller who omits the field means.
type AutoInt struct {
	// Set reports whether an explicit value was supplied.
	Set bool
	// Value is meaningful only when Set is true.
	Value int
}

// Auto is the automatic AutoInt, and the zero value of the type.
var Auto = AutoInt{}

// IsZero reports whether the value is automatic, which drives `omitzero`.
func (a AutoInt) IsZero() bool { return !a.Set }

// MarshalJSON emits the number, or "auto" when unset.
func (a AutoInt) MarshalJSON() ([]byte, error) {
	if !a.Set {
		return []byte(`"auto"`), nil
	}
	return json.Marshal(a.Value)
}

// UnmarshalJSON accepts a number or the string "auto".
func (a *AutoInt) UnmarshalJSON(b []byte) error {
	raw := strings.TrimSpace(string(b))
	if raw == "null" {
		*a = Auto
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		return a.parse(s)
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("expected an integer or \"auto\"")
	}
	*a = AutoInt{Set: true, Value: n}
	return nil
}

// parse reads the string form used by query parameters and multipart fields.
func (a *AutoInt) parse(s string) error {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "auto" {
		*a = Auto
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("expected an integer or \"auto\"")
	}
	*a = AutoInt{Set: true, Value: n}
	return nil
}

// String renders the value as it is accepted, for echoing in diagnostics.
func (a AutoInt) String() string {
	if !a.Set {
		return "auto"
	}
	return strconv.Itoa(a.Value)
}
