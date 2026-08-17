package builder

import (
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/el-amin-dev/barqr/internal/mapsurl"
)

// Location is the registry name of the Google Maps location builder.
const Location = "location"

func init() { Register(locationBuilder{}) }

// LocationPayload carries a place to open in Google Maps.
type LocationPayload struct {
	Location string `json:"location"`
	Origin   string `json:"origin"`
	Mode     string `json:"mode"`
	Zoom     int    `json:"zoom"`
	Language string `json:"language"`
	Region   string `json:"region"`
}

// Google's zoom scale: 1 shows the whole globe, 21 is the closest street level
// the tile server will serve. Anything outside it renders as a broken map.
const (
	zoomMin = 1
	zoomMax = 21
)

// locationModes are the travel modes the Maps directions API accepts. Google
// ignores an unknown travelmode rather than reporting it, which would turn a
// typo into a silently wrong route, so the set is checked here instead.
var locationModes = []string{"driving", "walking", "bicycling", "transit"}

// The three URL shapes this builder emits and is therefore able to read back.
// Google publishes many more, but only these carry the original query in a
// parameter, which is what makes the round trip possible at all.
const (
	locationHost       = "www.google.com"
	locationSearchPath = "/maps/search/"
	locationDirPath    = "/maps/dir/"
	locationCoordsPath = "/maps"
)

// locationBuilder turns whatever a person typed into a link that opens the
// place in Google Maps, so a printed code takes a stranger to a door rather
// than to a string of numbers they have to retype.
//
// The detection itself lives in internal/mapsurl: an address in any script,
// coordinates in a dozen notations, a plus code, a geo: URI, or a link already
// pasted from Google, Apple, Waze, OSM or Bing all arrive here as one free-form
// field and leave as one URL.
//
// Parse is narrower than Build on purpose. Build accepts everything mapsurl can
// recognise, but only the search, directions and ?q=&z= forms name their input
// in a query parameter; a shortened goo.gl link or a passed-through foreign map
// URL has nothing to recover a payload from. Claiming those would break the
// invariant that a parsed payload rebuilds to the string it came from.
type locationBuilder struct{}

func (locationBuilder) Name() string { return Location }

func (locationBuilder) Fields() []Field {
	return []Field{
		{
			Name:        "location",
			Type:        TypeString,
			Description: "address in any script, coordinates, plus code, geo: URI or map link",
			Required:    true,
			Example:     "Nelson's Column, London",
		},
		{
			Name:        "origin",
			Type:        TypeString,
			Description: "start of the journey; set it to build a directions link",
			Example:     "Trafalgar Square, London",
		},
		{
			Name:        "mode",
			Type:        TypeString,
			Description: "travel mode: driving, walking, bicycling or transit",
			Example:     "walking",
		},
		{
			// No example: zoom only reaches a URL built from coordinates, and
			// the location example above is an address whose directions link
			// has nowhere to put it. Sample value: 18.
			Name:        "zoom",
			Type:        TypeNumber,
			Description: "map zoom 1 to 21, honoured for coordinates only, e.g. 18",
		},
		{
			Name:        "language",
			Type:        TypeString,
			Description: "interface language code, the hl= parameter",
			Example:     "en",
		},
		{
			Name:        "region",
			Type:        TypeString,
			Description: "region bias code, the gl= parameter",
			Example:     "gb",
		},
	}
}

func (b locationBuilder) Build(payload any) (string, error) {
	m, err := payloadMap(payload, b.Fields())
	if err != nil {
		return "", err
	}

	loc, err := strReq(m, "location")
	if err != nil {
		return "", err
	}
	opts, err := locationOptions(m)
	if err != nil {
		return "", err
	}

	r := mapsurl.Resolve(loc, mapsurl.WithOptions(opts))
	// A location of nothing but bidi marks or zero-width spaces survives
	// strReq, because none of them is whitespace to TrimSpace, but normalises
	// away to nothing. Returning the empty URL would encode a code that opens
	// a blank map.
	if r.Kind == mapsurl.KindEmpty || r.URL == "" {
		return "", fmt.Errorf("%w: location %q names no place to look up",
			ErrInvalidPayload, loc)
	}
	return r.URL, nil
}

// Describe reports what the resolver made of the input.
//
// The URL alone hides the interesting part: whether "35.95, 5.53" was read as
// a coordinate pair or fell through to a text search, and how sure the
// resolver was. A caller deciding what to do next — render a code, show a
// confirmation, ask the user to be more specific — needs that, and it is
// already computed on the way to the URL.
func (b locationBuilder) Describe(payload any) (map[string]any, error) {
	m, err := payloadMap(payload, b.Fields())
	if err != nil {
		return nil, err
	}

	loc, err := strReq(m, "location")
	if err != nil {
		return nil, err
	}
	opts, err := locationOptions(m)
	if err != nil {
		return nil, err
	}

	r := mapsurl.Resolve(loc, mapsurl.WithOptions(opts))

	out := map[string]any{
		"kind":       string(r.Kind),
		"confidence": r.Confidence,
		"url":        r.URL,
	}
	// Only report what was actually recovered: a nil coordinate is meaningful
	// and must not appear as 0,0, which is a real place in the Atlantic.
	if r.HasCoords() {
		out["lat"], out["lng"] = *r.Lat, *r.Lng
	}
	if r.Query != "" {
		out["query"] = r.Query
	}
	if r.Normalized != "" && r.Normalized != loc {
		out["normalized"] = r.Normalized
	}
	if len(r.Notes) > 0 {
		out["notes"] = r.Notes
	}
	return out, nil
}

// locationOptions reads the tuning fields and rejects the values Google would
// quietly ignore.
func locationOptions(m map[string]any) (mapsurl.Options, error) {
	var o mapsurl.Options

	origin, err := str(m, "origin")
	if err != nil {
		return o, err
	}
	o.Origin = strings.TrimSpace(origin)

	mode, err := str(m, "mode")
	if err != nil {
		return o, err
	}
	o.Mode = strings.TrimSpace(mode)
	if o.Mode != "" && !slices.Contains(locationModes, o.Mode) {
		return o, fmt.Errorf("%w: mode %q is not one of %s",
			ErrInvalidPayload, o.Mode, strings.Join(locationModes, ", "))
	}

	zoom, present, err := number(m, "zoom")
	if err != nil {
		return o, err
	}
	if present {
		if zoom != math.Trunc(zoom) || zoom < zoomMin || zoom > zoomMax {
			return o, fmt.Errorf("%w: zoom %s is not a whole number from %d to %d",
				ErrInvalidPayload, formatNumber(zoom), zoomMin, zoomMax)
		}
		o.Zoom = int(zoom)
	}

	language, err := str(m, "language")
	if err != nil {
		return o, err
	}
	o.Language = strings.TrimSpace(language)

	region, err := str(m, "region")
	if err != nil {
		return o, err
	}
	o.Region = strings.TrimSpace(region)

	return o, nil
}

func (b locationBuilder) Parse(raw string) (any, bool) {
	m, ok := parseLocationURL(raw)
	if !ok {
		return nil, false
	}

	// The whole-string check that stableString performs per field. Build
	// normalises hard — "35.95, 5.53" becomes "35.9500000,5.5300000", and the
	// parameters come out in one fixed order — so comparing the rebuilt URL
	// against the original is the only way to be sure this payload is the one
	// that produced it. It subsumes the per-field stability test: a location,
	// zoom or mode that Build would have rewritten fails here.
	rebuilt, err := b.Build(m)
	if err != nil || rebuilt != raw {
		return nil, false
	}
	return m, true
}

// parseLocationURL recovers the payload fields from one of the three URL forms
// Build emits. It is deliberately literal: anything else, including a shortened
// link or another provider's map URL, belongs to no payload this builder can
// rebuild.
func parseLocationURL(raw string) (map[string]any, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != locationHost {
		return nil, false
	}

	q := u.Query()
	out := map[string]any{}

	switch u.Path {
	case locationSearchPath:
		out["location"] = q.Get("query")
	case locationDirPath:
		out["location"] = q.Get("destination")
		if v := q.Get("origin"); v != "" {
			out["origin"] = v
		}
		if v := q.Get("travelmode"); v != "" {
			out["mode"] = v
		}
	case locationCoordsPath:
		out["location"] = q.Get("q")
		z, convErr := strconv.Atoi(q.Get("z"))
		if convErr != nil {
			return nil, false
		}
		// float64 to match what number reads back out of a JSON body, so that
		// a payload parsed here compares equal to one decoded from a request.
		out["zoom"] = float64(z)
	default:
		return nil, false
	}

	if v := q.Get("hl"); v != "" {
		out["language"] = v
	}
	if v := q.Get("gl"); v != "" {
		out["region"] = v
	}

	if loc, _ := out["location"].(string); loc == "" {
		return nil, false
	}
	// Build trims every text field, so a recovered value carrying edge
	// whitespace would rebuild into a different URL.
	if !trimmedValues(out) {
		return nil, false
	}
	return out, true
}
