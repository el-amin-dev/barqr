// Package mapsurl turns any user-supplied location string into a Google Maps URL.
//
// It accepts free-form addresses (any script), decimal or DMS coordinates,
// Open Location Codes (plus codes), geo: URIs, links from Google/Apple/Waze/
// OSM/Bing, and "from A to B" directions requests.
//
// Basic use:
//
//	u := mapsurl.Get("35.95277, 5.53753")
//	r := mapsurl.Resolve("from Setif to Algiers", mapsurl.WithMode("driving"))
//
// The detection pipeline is an ordered registry; add your own types with
// Register without touching the core.
package mapsurl

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// result type
// ---------------------------------------------------------------------------

// Kind is the detected category of an input string.
type Kind string

// The categories the pipeline can report. The order below is the order the
// default handlers are tried in, so the more specific shapes win over the
// free-text fallback.
const (
	// KindEmpty means the input held nothing once normalization was applied.
	KindEmpty Kind = "empty"
	// KindDirections means the input named both an origin and a destination.
	KindDirections Kind = "directions"
	// KindGeoURI means the input was an RFC 5870 geo: URI.
	KindGeoURI Kind = "geo_uri"
	// KindLink means the input was a URL that is handed on unchanged.
	KindLink Kind = "link"
	// KindMapLink means coordinates were recovered from a non-Google map link.
	KindMapLink Kind = "map_link"
	// KindPlusCode means the input was an Open Location Code.
	KindPlusCode Kind = "plus_code"
	// KindCoordinates means the input was a latitude/longitude pair.
	KindCoordinates Kind = "coordinates"
	// KindAddress means nothing more specific matched, so the text is searched.
	KindAddress Kind = "address"
)

// Resolution is the full outcome of resolving one input string.
type Resolution struct {
	// Kind is the category the pipeline settled on.
	Kind Kind
	// URL is the Google Maps link to encode or open.
	URL string
	// Raw is the input exactly as it arrived.
	Raw string
	// Normalized is Raw after digit, punctuation and whitespace folding.
	Normalized string
	// Lat and Lng are nil when the input carried no coordinates.
	Lat, Lng *float64
	// Query is the text sent to Google, for the kinds that search.
	Query string
	// Confidence is 1 for an unambiguous shape and lower for a guess.
	Confidence float64
	// Notes explain a decision that is not obvious from Kind alone.
	Notes []string
}

// String makes a Resolution usable anywhere the URL string is expected.
func (r Resolution) String() string { return r.URL }

// HasCoords reports whether latitude and longitude were recovered.
func (r Resolution) HasCoords() bool { return r.Lat != nil && r.Lng != nil }

func f64(v float64) *float64 { return &v }

// ---------------------------------------------------------------------------
// options
// ---------------------------------------------------------------------------

// Options tunes URL construction. The zero value is the sensible default:
// Google links pass through untouched and no extra parameters are added.
type Options struct {
	// Zoom is 1..21 and switches coordinates to the ?q=&z= form.
	Zoom int
	// Mode is driving, walking, bicycling or transit.
	Mode string
	// Origin forces a directions URL from that place.
	Origin string
	// Language becomes the hl= parameter.
	Language string
	// Region becomes the gl= parameter.
	Region string
	// RewriteGoogleLinks rebuilds Google links instead of passing them through.
	RewriteGoogleLinks bool
}

// Option applies a single setting.
type Option func(*Options)

// WithZoom sets the zoom level. It only reaches URLs built from coordinates,
// because the api=1 search form has no zoom parameter.
func WithZoom(z int) Option { return func(o *Options) { o.Zoom = z } }

// WithMode sets the travel mode used when a directions URL is built.
func WithMode(m string) Option { return func(o *Options) { o.Mode = m } }

// WithOrigin forces a directions URL starting at s.
func WithOrigin(s string) Option { return func(o *Options) { o.Origin = s } }

// WithLanguage sets the hl= interface language.
func WithLanguage(l string) Option { return func(o *Options) { o.Language = l } }

// WithRegion sets the gl= region bias.
func WithRegion(r string) Option { return func(o *Options) { o.Region = r } }

// RewriteGoogleLinks rebuilds Google links from their coordinates rather than
// passing them through, which is what a caller wants when the other options
// have to be applied to an already-Google URL.
func RewriteGoogleLinks() Option { return func(o *Options) { o.RewriteGoogleLinks = true } }

// WithOptions replaces the whole option set at once, for a caller that already
// holds a filled-in Options.
func WithOptions(v Options) Option { return func(o *Options) { *o = v } }

// ---------------------------------------------------------------------------
// normalization (detection only — addresses are sent as the user typed them)
// ---------------------------------------------------------------------------

var punctMap = map[rune]string{
	'،': ",", '，': ",", '﹐': ",", '٫': ".", '؛': ";", '；': ";",
	'º': "°", '˚': "°", '∘': "°",
	'’': "'", '‘': "'", '′': "'", 'ʹ': "'",
	'”': `"`, '“': `"`, '″': `"`,
	'–': "-", '—': "-", '−': "-",
	'\u00a0': " ", '\u200f': "", '\u200e': "", '\ufeff': "",
}

var spaceRe = regexp.MustCompile(`\s+`)

// normDigit folds Arabic-Indic, Extended Arabic-Indic and fullwidth digits to ASCII.
func normDigit(r rune) (rune, bool) {
	switch {
	case r >= '\u0660' && r <= '\u0669':
		return '0' + (r - '\u0660'), true
	case r >= '\u06F0' && r <= '\u06F9':
		return '0' + (r - '\u06F0'), true
	case r >= '\uFF10' && r <= '\uFF19':
		return '0' + (r - '\uFF10'), true
	}
	return r, false
}

// Normalize folds digits, punctuation and whitespace so detection can rely on
// a single canonical shape. It is never used to build address queries.
func Normalize(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if d, ok := normDigit(r); ok {
			b.WriteRune(d)
			continue
		}
		if s, ok := punctMap[r]; ok {
			b.WriteString(s)
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(spaceRe.ReplaceAllString(b.String(), " "))
}

// ---------------------------------------------------------------------------
// patterns
// ---------------------------------------------------------------------------

var (
	urlRe    = regexp.MustCompile(`(?i)(?:https?://|www\.|(?:[\w-]+\.)+[a-z]{2,}/)\S*`)
	geoURIRe = regexp.MustCompile(`(?i)^geo:(-?\d+(?:\.\d+)?),(-?\d+(?:\.\d+)?)(?:,-?\d+(?:\.\d+)?)?(?:\?.*)?$`)
	labelRe  = regexp.MustCompile(`(?i)\b(lat(itude)?|lng|lon(gitude)?|long|y|x)\s*[:=]\s*`)
	plusRe   = regexp.MustCompile(`(?i)^(?P<code>[23456789CFGHJMPQRVWX]{2,8}\+[23456789CFGHJMPQRVWX]{0,3})(?:\s+(?P<locality>.+))?$`)
	fromToRe = regexp.MustCompile(`(?i)^\s*from\s+(.+?)\s+to\s+(.+?)\s*$`)
	arrowRe  = regexp.MustCompile(`\s*(?:->|=>|→)\s*`)

	atRe   = regexp.MustCompile(`[@/](-?\d{1,3}\.\d+),\s*(-?\d{1,3}\.\d+)`)
	osmRe  = regexp.MustCompile(`#map=\d+(?:\.\d+)?/(-?\d+\.\d+)/(-?\d+\.\d+)`)
	bingRe = regexp.MustCompile(`(?i)[?&]cp=(-?\d+\.\d+)~(-?\d+\.\d+)`)
)

// mapHosts are hosts whose links describe a place.
var mapHosts = []string{
	"google.com/maps", "maps.google.", "goo.gl/maps", "maps.app.goo.gl",
	"g.co/kgs", "openstreetmap.org", "osm.org", "maps.apple.com",
	"waze.com", "bing.com/maps", "here.com", "yandex.", "2gis.", "mapy.cz",
}

var googleHosts = []string{"google.com", "goo.gl", "g.co"}

var googleCcTLDRe = regexp.MustCompile(`(^|\.)google\.[a-z.]{2,}$`)

func dmsPart(p string) string {
	return fmt.Sprintf(
		`(?:(?P<%[1]sh1>[NSEWnsew])\s*)?`+
			`(?P<%[1]sd>[-+]?\d{1,3}(?:\.\d+)?)\s*(?:°|:)?\s*`+
			`(?:(?P<%[1]sm>\d{1,2}(?:\.\d+)?)\s*(?:'|:)\s*)?`+
			`(?:(?P<%[1]ss>\d{1,2}(?:\.\d+)?)\s*(?:"|'')?\s*)?`+
			`(?:(?P<%[1]sh2>[NSEWnsew]))?`, p)
}

var coordPairRe = regexp.MustCompile(
	`^` + dmsPart("a") + `\s*(?P<sep>[,;/]|\s)\s*` + dmsPart("b") + `$`)

// groups turns a submatch slice into a name→value map.
func groups(re *regexp.Regexp, m []string) map[string]string {
	out := make(map[string]string, len(m))
	for i, name := range re.SubexpNames() {
		if name != "" && i < len(m) {
			out[name] = m[i]
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// parsers
// ---------------------------------------------------------------------------

func toDecimal(d, m, s, hemi string) (float64, error) {
	deg, err := strconv.ParseFloat(d, 64)
	if err != nil {
		return 0, err
	}
	// Not named min/max: revive's redefines-builtin-id rule rejects shadowing
	// the builtins, and this file is linted with that rule on.
	minutes, sec := 0.0, 0.0
	if m != "" {
		minutes, _ = strconv.ParseFloat(m, 64)
	}
	if s != "" {
		sec, _ = strconv.ParseFloat(s, 64)
	}
	val := abs(deg) + minutes/60 + sec/3600
	h := strings.ToUpper(hemi)
	if deg < 0 || h == "S" || h == "W" {
		val = -val
	}
	return val, nil
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ParseCoordinates accepts decimal, DMS, degree-minute, hemisphere-suffixed,
// labeled and non-ASCII-digit coordinate pairs. ok is false when the text is
// not a coordinate pair or falls outside valid lat/lng ranges.
func ParseCoordinates(text string) (lat, lng float64, ok bool) {
	s := strings.TrimSpace(labelRe.ReplaceAllString(Normalize(text), ""))
	m := coordPairRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	g := groups(coordPairRe, m)

	hasHemi := g["ah1"]+g["ah2"]+g["bh1"]+g["bh2"] != ""
	hasDMS := strings.ContainsAny(s, `°'"`)
	// a bare space separator is too weak on its own: "45 12" is not a location
	if g["sep"] == " " && !hasHemi && !hasDMS {
		return 0, 0, false
	}

	a, err := toDecimal(g["ad"], g["am"], g["as"], firstNonEmpty(g["ah1"], g["ah2"]))
	if err != nil {
		return 0, 0, false
	}
	b, err := toDecimal(g["bd"], g["bm"], g["bs"], firstNonEmpty(g["bh1"], g["bh2"]))
	if err != nil {
		return 0, 0, false
	}

	lat, lng = a, b
	// honour explicit hemispheres when the user wrote longitude first
	switch strings.ToUpper(firstNonEmpty(g["ah1"], g["ah2"])) {
	case "E", "W":
		lat, lng = b, a
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, false
	}
	return lat, lng, true
}

// ParsePlusCode validates an Open Location Code. Short codes (fewer than eight
// characters before the '+') are rejected unless a locality follows them.
func ParsePlusCode(text string) (code, locality string, ok bool) {
	m := plusRe.FindStringSubmatch(Normalize(text))
	if m == nil {
		return "", "", false
	}
	g := groups(plusRe, m)
	code = strings.ToUpper(g["code"])
	head := strings.SplitN(code, "+", 2)[0]
	if len(head) < 2 || len(head)%2 != 0 {
		return "", "", false
	}
	locality = g["locality"]
	if len(head) < 8 && locality == "" {
		return "", "", false
	}
	return code, locality, true
}

// ExtractURL pulls the first URL out of arbitrary text, adding a scheme when
// the user pasted a bare host.
func ExtractURL(text string) (string, bool) {
	m := urlRe.FindString(Normalize(text))
	if m == "" {
		return "", false
	}
	m = strings.TrimRight(m, `).,;'"`)
	if !strings.HasPrefix(strings.ToLower(m), "http") {
		m = "https://" + m
	}
	return m, true
}

// IsMapLink reports whether a URL points at a known mapping service.
func IsMapLink(u string) bool {
	low := strings.ToLower(u)
	for _, h := range mapHosts {
		if strings.Contains(low, h) {
			return true
		}
	}
	return false
}

// IsGoogleLink matches on label boundaries, so bing.com never matches g.co.
func IsGoogleLink(raw string) bool {
	p, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(p.Hostname()), ".")
	for _, h := range googleHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return googleCcTLDRe.MatchString(host)
}

var paramKeys = []string{"ll", "q", "query", "center", "destination", "daddr", "sll"}

// CoordsFromURL recovers a coordinate pair from a map link of any provider.
func CoordsFromURL(raw string) (lat, lng float64, ok bool) {
	for _, re := range []*regexp.Regexp{osmRe, bingRe} {
		if m := re.FindStringSubmatch(raw); m != nil {
			a, err1 := strconv.ParseFloat(m[1], 64)
			b, err2 := strconv.ParseFloat(m[2], 64)
			if err1 == nil && err2 == nil {
				return a, b, true
			}
		}
	}
	if p, err := url.Parse(raw); err == nil {
		q := p.Query()
		if mlat, mlon := q.Get("mlat"), q.Get("mlon"); mlat != "" && mlon != "" {
			a, err1 := strconv.ParseFloat(mlat, 64)
			b, err2 := strconv.ParseFloat(mlon, 64)
			if err1 == nil && err2 == nil {
				return a, b, true
			}
		}
		for _, key := range paramKeys {
			for _, v := range q[key] {
				if a, b, okCoord := ParseCoordinates(v); okCoord {
					return a, b, true
				}
			}
		}
	}
	if m := atRe.FindStringSubmatch(raw); m != nil {
		a, err1 := strconv.ParseFloat(m[1], 64)
		b, err2 := strconv.ParseFloat(m[2], 64)
		if err1 == nil && err2 == nil {
			return a, b, true
		}
	}
	return 0, 0, false
}

// ---------------------------------------------------------------------------
// builders
// ---------------------------------------------------------------------------

// escape percent-encodes for a query value. url.QueryEscape renders spaces as
// '+', which is legal but confusing next to plus codes, so spaces become %20.
func escape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func extras(o Options) string {
	var parts []string
	if o.Language != "" {
		parts = append(parts, "hl="+escape(o.Language))
	}
	if o.Region != "" {
		parts = append(parts, "gl="+escape(o.Region))
	}
	if len(parts) == 0 {
		return ""
	}
	return "&" + strings.Join(parts, "&")
}

// BuildSearch renders a place-search URL for free text.
func BuildSearch(query string, o Options) string {
	return "https://www.google.com/maps/search/?api=1&query=" + escape(query) + extras(o)
}

func fmtCoord(f float64) string { return strconv.FormatFloat(f, 'f', 7, 64) }

// BuildCoords renders a coordinate URL, using the legacy ?q=&z= form when a
// zoom level is requested (the api=1 search form has no zoom parameter).
func BuildCoords(lat, lng float64, o Options) string {
	if o.Zoom > 0 {
		z := o.Zoom
		if z > 21 {
			z = 21
		}
		return fmt.Sprintf("https://www.google.com/maps?q=%s,%s&z=%d%s",
			fmtCoord(lat), fmtCoord(lng), z, extras(o))
	}
	return BuildSearch(fmtCoord(lat)+","+fmtCoord(lng), o)
}

// BuildDirections renders a turn-by-turn URL; origin may be empty.
func BuildDirections(destination, origin string, o Options) string {
	u := "https://www.google.com/maps/dir/?api=1&destination=" + escape(destination)
	if origin != "" {
		u += "&origin=" + escape(origin)
	}
	if o.Mode != "" {
		u += "&travelmode=" + escape(o.Mode)
	}
	return u + extras(o)
}

// ---------------------------------------------------------------------------
// handler registry
// ---------------------------------------------------------------------------

// Handler inspects the raw and normalized input and returns a Resolution, or
// nil to pass the input down to the next handler.
//
// Every handler takes the full Options even when it ignores them, because the
// signature is the extension point: a third-party handler registered through
// Register has to be able to honour zoom, language and region without the
// registry needing a second handler shape.
type Handler func(raw, norm string, o Options) *Resolution

type entry struct {
	priority int
	kind     Kind
	fn       Handler
}

var (
	mu       sync.RWMutex
	handlers []entry
)

// Register plugs a handler into the pipeline. Lower priority runs first.
func Register(kind Kind, priority int, fn Handler) {
	mu.Lock()
	defer mu.Unlock()
	handlers = append(handlers, entry{priority, kind, fn})
	sort.SliceStable(handlers, func(i, j int) bool {
		return handlers[i].priority < handlers[j].priority
	})
}

func res(kind Kind, u, raw, norm string) *Resolution {
	return &Resolution{Kind: kind, URL: u, Raw: raw, Normalized: norm, Confidence: 1}
}

func init() {
	Register(KindEmpty, 10, handleEmpty)
	Register(KindDirections, 20, handleDirections)
	Register(KindGeoURI, 30, handleGeoURI)
	Register(KindLink, 40, handleLink)
	Register(KindPlusCode, 50, handlePlusCode)
	Register(KindCoordinates, 60, handleCoordinates)
	Register(KindAddress, 999, handleAddress)
}

// handleEmpty ignores o: a blank input has nothing for zoom or language to
// apply to, and the zero-confidence result exists only to name the failure.
// The parameter stays in the signature because Handler is the extension point.
func handleEmpty(raw, norm string, o Options) *Resolution {
	if norm != "" {
		return nil
	}
	r := res(KindEmpty, "", raw, norm)
	r.Confidence = 0
	r.Notes = []string{"blank input"}
	return r
}

func handleDirections(raw, norm string, o Options) *Resolution {
	origin, dest := o.Origin, ""
	switch {
	case fromToRe.MatchString(norm):
		m := fromToRe.FindStringSubmatch(norm)
		origin, dest = m[1], m[2]
	case arrowRe.MatchString(norm):
		parts := arrowRe.Split(norm, -1)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			origin, dest = parts[0], parts[1]
		}
	case origin != "":
		dest = strings.TrimSpace(raw)
	}
	if dest == "" {
		return nil
	}
	r := res(KindDirections, BuildDirections(dest, origin, o), raw, norm)
	r.Query = dest
	r.Notes = []string{fmt.Sprintf("origin=%q", origin)}
	return r
}

func handleGeoURI(raw, norm string, o Options) *Resolution {
	m := geoURIRe.FindStringSubmatch(norm)
	if m == nil {
		return nil
	}
	lat, err1 := strconv.ParseFloat(m[1], 64)
	lng, err2 := strconv.ParseFloat(m[2], 64)
	if err1 != nil || err2 != nil {
		return nil
	}
	r := res(KindGeoURI, BuildCoords(lat, lng, o), raw, norm)
	r.Lat, r.Lng = f64(lat), f64(lng)
	return r
}

func handleLink(raw, norm string, o Options) *Resolution {
	u, found := ExtractURL(norm)
	if !found {
		return nil
	}
	mapish := IsMapLink(u)
	lat, lng, hasCoords := 0.0, 0.0, false
	if mapish {
		lat, lng, hasCoords = CoordsFromURL(u)
	}

	if mapish && IsGoogleLink(u) && !o.RewriteGoogleLinks {
		r := res(KindLink, u, raw, norm)
		if hasCoords {
			r.Lat, r.Lng = f64(lat), f64(lng)
		}
		r.Notes = []string{"google link passed through unchanged"}
		return r
	}
	if hasCoords {
		r := res(KindMapLink, BuildCoords(lat, lng, o), raw, norm)
		r.Lat, r.Lng = f64(lat), f64(lng)
		r.Notes = []string{"coordinates extracted from foreign map link"}
		return r
	}
	r := res(KindLink, u, raw, norm)
	if mapish {
		r.Confidence = 0.6
		r.Notes = []string{"map host but no coordinates found — passed through"}
	} else {
		r.Confidence = 0.4
		r.Notes = []string{"not a map URL — passed through unchanged"}
	}
	return r
}

func handlePlusCode(raw, norm string, o Options) *Resolution {
	code, locality, ok := ParsePlusCode(norm)
	if !ok {
		return nil
	}
	query := code
	if locality != "" {
		query += " " + locality
	}
	r := res(KindPlusCode, BuildSearch(query, o), raw, norm)
	r.Query = query
	return r
}

func handleCoordinates(raw, norm string, o Options) *Resolution {
	lat, lng, ok := ParseCoordinates(norm)
	if !ok {
		return nil
	}
	r := res(KindCoordinates, BuildCoords(lat, lng, o), raw, norm)
	r.Lat, r.Lng = f64(lat), f64(lng)
	return r
}

func handleAddress(raw, norm string, o Options) *Resolution {
	// the RAW text is sent, so Arabic and accents reach Google exactly as typed
	query := strings.TrimSpace(raw)
	r := res(KindAddress, BuildSearch(query, o), raw, norm)
	r.Query = query
	r.Confidence = 0.8
	return r
}

// ---------------------------------------------------------------------------
// entry points
// ---------------------------------------------------------------------------

// Resolve runs the pipeline and returns the full outcome.
func Resolve(text string, opts ...Option) Resolution {
	var o Options
	for _, fn := range opts {
		fn(&o)
	}
	norm := Normalize(text)

	mu.RLock()
	snapshot := make([]entry, len(handlers))
	copy(snapshot, handlers)
	mu.RUnlock()

	for _, e := range snapshot {
		if r := e.fn(text, norm, o); r != nil {
			return *r
		}
	}
	return *handleAddress(text, norm, o)
}

// Get returns just the URL. It is the one-liner most callers want.
func Get(text string, opts ...Option) string { return Resolve(text, opts...).URL }

// Classify returns only the detected kind.
func Classify(text string, opts ...Option) Kind { return Resolve(text, opts...).Kind }
