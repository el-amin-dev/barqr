package mapsurl_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/el-amin-dev/barqr/internal/mapsurl"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		in   string
		want mapsurl.Kind
	}{
		// ---- the original six ------------------------------------------
		{"35.95277287790832, 5.537532012437717", mapsurl.KindCoordinates},
		{"https://maps.app.goo.gl/6Xz63Mu1nqHgc1o89", mapsurl.KindLink},
		{"GQ62+CW Quero, Spain", mapsurl.KindPlusCode},
		{"45 Rue Didouche Mourad, Alger", mapsurl.KindAddress},
		{"45 شارع ديدوش مراد، الجزائر العاصمة", mapsurl.KindAddress},
		{"45 Didouche Mourad Street, Algiers", mapsurl.KindAddress},
		// ---- coordinate variants ---------------------------------------
		{"36, 5", mapsurl.KindCoordinates},
		{"  35.95 ,  5.53  ", mapsurl.KindCoordinates},
		{"35.95;5.53", mapsurl.KindCoordinates},
		{"35.95N, 5.53E", mapsurl.KindCoordinates},
		{"35.95 S 5.53 W", mapsurl.KindCoordinates},
		{"5.53E 35.95N", mapsurl.KindCoordinates},
		{`35°57'10.0"N 5°32'15.1"E`, mapsurl.KindCoordinates},
		{"36°10.5'N 5°25.2'W", mapsurl.KindCoordinates},
		{"lat: 35.95, lng: 5.53", mapsurl.KindCoordinates},
		{"٣٥.٩٥، ٥.٥٣", mapsurl.KindCoordinates},
		{"-33.8688, 151.2093", mapsurl.KindCoordinates},
		{"999.9, 5.5", mapsurl.KindAddress},
		{"45 12", mapsurl.KindAddress},
		{"geo:35.95,5.53?z=17", mapsurl.KindGeoURI},
		// ---- links -------------------------------------------------------
		{"https://www.google.com/maps/@35.95,5.53,17z", mapsurl.KindLink},
		{"https://www.google.com/maps/place/Setif/@36.19,5.41,14z/data=!3m1", mapsurl.KindLink},
		{"check this out https://maps.app.goo.gl/abc123 thanks", mapsurl.KindLink},
		{"maps.google.com/?q=35.95,5.53", mapsurl.KindLink},
		{"https://maps.apple.com/?ll=35.95,5.53&q=Setif", mapsurl.KindMapLink},
		{"https://www.waze.com/ul?ll=35.95,5.53&navigate=yes", mapsurl.KindMapLink},
		{"https://www.openstreetmap.org/#map=17/35.95/5.53", mapsurl.KindMapLink},
		{"https://www.bing.com/maps?cp=35.95~5.53&lvl=17", mapsurl.KindMapLink},
		{"https://example.com/page", mapsurl.KindLink},
		// ---- plus codes --------------------------------------------------
		{"8FVC9G8F+6W", mapsurl.KindPlusCode},
		{"gq62+cw quero, spain", mapsurl.KindPlusCode},
		{"GQ62+CW", mapsurl.KindAddress},
		// ---- directions --------------------------------------------------
		{"from Setif to Algiers", mapsurl.KindDirections},
		{"Setif -> Algiers", mapsurl.KindDirections},
		{"Bejaia → Setif", mapsurl.KindDirections},
		// ---- edges -------------------------------------------------------
		{"", mapsurl.KindEmpty},
		{"   ", mapsurl.KindEmpty},
		{"Café René, Sétif", mapsurl.KindAddress},
		{"pharmacy near me", mapsurl.KindAddress},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := mapsurl.Resolve(c.in)
			if got.Kind != c.want {
				t.Fatalf("kind = %q, want %q (url=%s)", got.Kind, c.want, got.URL)
			}
			if c.want != mapsurl.KindEmpty && got.URL == "" {
				t.Fatalf("empty URL for %q", c.in)
			}
		})
	}
}

func TestCoordinateValues(t *testing.T) {
	cases := []struct {
		in               string
		wantLat, wantLng float64
	}{
		{"36, 5", 36, 5},
		{"35.95 S 5.53 W", -35.95, -5.53},
		{"5.53E 35.95N", 35.95, 5.53},
		{`35°57'10.0"N 5°32'15.1"E`, 35.952777, 5.537527},
		{"36°10.5'N 5°25.2'W", 36.175, -5.42},
		{"٣٥.٩٥، ٥.٥٣", 35.95, 5.53},
		{"geo:35.95,5.53?z=17", 35.95, 5.53},
		{"https://www.bing.com/maps?cp=35.95~5.53&lvl=17", 35.95, 5.53},
		{"https://www.openstreetmap.org/#map=17/35.95/5.53", 35.95, 5.53},
	}
	for _, c := range cases {
		r := mapsurl.Resolve(c.in)
		if !r.HasCoords() {
			t.Fatalf("%q: no coordinates", c.in)
		}
		if diff := *r.Lat - c.wantLat; diff > 1e-4 || diff < -1e-4 {
			t.Errorf("%q: lat = %v, want %v", c.in, *r.Lat, c.wantLat)
		}
		if diff := *r.Lng - c.wantLng; diff > 1e-4 || diff < -1e-4 {
			t.Errorf("%q: lng = %v, want %v", c.in, *r.Lng, c.wantLng)
		}
	}
}

func TestPlusCodeSignSurvivesEncoding(t *testing.T) {
	u := mapsurl.Get("GQ62+CW Quero, Spain")
	if !strings.Contains(u, "GQ62%2BCW") {
		t.Fatalf("plus sign not encoded as %%2B: %s", u)
	}
	if strings.Contains(u, "GQ62+CW") {
		t.Fatalf("literal '+' would decode as a space: %s", u)
	}
}

func TestUnicodeAddressKeepsRawText(t *testing.T) {
	r := mapsurl.Resolve("45 شارع ديدوش مراد، الجزائر العاصمة")
	if r.Query != "45 شارع ديدوش مراد، الجزائر العاصمة" {
		t.Fatalf("raw text was mangled: %q", r.Query)
	}
}

func TestBingIsNotMistakenForGoogle(t *testing.T) {
	// "bing.com" contains the substring "g.co" — a naive check gets this wrong
	if r := mapsurl.Resolve("https://www.bing.com/maps?cp=35.95~5.53&lvl=17"); r.Kind != mapsurl.KindMapLink {
		t.Fatalf("kind = %q, want map_link", r.Kind)
	}
}

func TestOptions(t *testing.T) {
	cases := []struct{ got, want string }{
		{mapsurl.Get("35.95, 5.53", mapsurl.WithZoom(18)),
			"https://www.google.com/maps?q=35.9500000,5.5300000&z=18"},
		{mapsurl.Get("Setif", mapsurl.WithOrigin("Algiers"), mapsurl.WithMode("driving")),
			"https://www.google.com/maps/dir/?api=1&destination=Setif&origin=Algiers&travelmode=driving"},
		{mapsurl.Get("Setif", mapsurl.WithLanguage("ar"), mapsurl.WithRegion("dz")),
			"https://www.google.com/maps/search/?api=1&query=Setif&hl=ar&gl=dz"},
		{mapsurl.Get("https://www.google.com/maps/@35.95,5.53,17z", mapsurl.RewriteGoogleLinks()),
			"https://www.google.com/maps/search/?api=1&query=35.9500000%2C5.5300000"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got  %s\nwant %s", c.got, c.want)
		}
	}
}

// TestWithOptionsSetsEveryFieldAtOnce covers the bulk option, which is the one
// a caller reaches for when the settings arrive as a struct rather than as
// individual calls.
func TestWithOptionsSetsEveryFieldAtOnce(t *testing.T) {
	got := mapsurl.Get("35.95, 5.53", mapsurl.WithOptions(mapsurl.Options{
		Zoom: 30, Language: "fr", Region: "dz",
	}))
	// Zoom is clamped to 21: Google rejects anything higher, and silently
	// capping beats emitting a URL that will not open.
	want := "https://www.google.com/maps?q=35.9500000,5.5300000&z=21&hl=fr&gl=dz"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// TestResolutionAccessors pins the two conveniences that let a Resolution be
// used where a plain string or a coordinate check is expected.
func TestResolutionAccessors(t *testing.T) {
	coords := mapsurl.Resolve("35.95, 5.53")
	if coords.String() != coords.URL {
		t.Errorf("String() = %q, want the URL %q", coords.String(), coords.URL)
	}
	if !coords.HasCoords() {
		t.Error("a coordinate resolution reports no coordinates")
	}
	if addr := mapsurl.Resolve("pharmacy near me"); addr.HasCoords() {
		t.Error("an address resolution claims coordinates")
	}
}

// TestNormalizeFoldsUnicodeNoise checks the folding in isolation, since every
// detector downstream depends on it collapsing to one canonical shape.
func TestNormalizeFoldsUnicodeNoise(t *testing.T) {
	cases := []struct{ in, want string }{
		{"٣٥٫٩٥، ٥٫٥٣", "35.95, 5.53"},
		{"３６，５", "36,5"},
		{"\u200f35.95\u200e", "35.95"},
		{"a  \t b", "a b"},
		{"35° 57′ N", `35° 57' N`},
	}
	for _, c := range cases {
		if got := mapsurl.Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParsersRejectNonLocations exercises the failure half of each parser,
// which is where a loose regexp would otherwise claim ordinary prose.
func TestParsersRejectNonLocations(t *testing.T) {
	for _, in := range []string{"pharmacy near me", "45 12", "999.9, 5.5", "36"} {
		if _, _, ok := mapsurl.ParseCoordinates(in); ok {
			t.Errorf("ParseCoordinates(%q) claimed a non-coordinate", in)
		}
	}
	// A short code with no locality cannot be resolved to a place, and an odd
	// number of characters before the '+' is not a valid code at all.
	for _, in := range []string{"GQ62+CW", "8FVC9G8+6W", "not a code"} {
		if _, _, ok := mapsurl.ParsePlusCode(in); ok {
			t.Errorf("ParsePlusCode(%q) claimed an invalid code", in)
		}
	}
	if _, ok := mapsurl.ExtractURL("no link here"); ok {
		t.Error("ExtractURL found a URL in plain prose")
	}
	if _, _, ok := mapsurl.CoordsFromURL("https://example.com/page"); ok {
		t.Error("CoordsFromURL invented coordinates")
	}
}

// TestExtractURLAddsAMissingScheme covers the bare-host case: users paste
// "maps.google.com/..." far more often than they paste a full URL.
func TestExtractURLAddsAMissingScheme(t *testing.T) {
	got, ok := mapsurl.ExtractURL("see maps.google.com/?q=35.95,5.53 for the spot")
	if !ok {
		t.Fatal("ExtractURL missed a bare host")
	}
	if want := "https://maps.google.com/?q=35.95,5.53"; got != want {
		t.Errorf("ExtractURL = %q, want %q", got, want)
	}
	if !mapsurl.IsMapLink(got) {
		t.Errorf("IsMapLink(%q) = false", got)
	}
	if !mapsurl.IsGoogleLink(got) {
		t.Errorf("IsGoogleLink(%q) = false", got)
	}
	if mapsurl.IsGoogleLink("https://www.bing.com/maps") {
		t.Error("IsGoogleLink matched bing.com")
	}
	// A country-code Google domain is still Google.
	if !mapsurl.IsGoogleLink("https://maps.google.co.uk/") {
		t.Error("IsGoogleLink missed a ccTLD Google domain")
	}
}

func TestCustomHandler(t *testing.T) {
	mapsurl.Register("what3words", 45, func(raw, norm string, o mapsurl.Options) *mapsurl.Resolution {
		if !strings.HasPrefix(norm, "///") {
			return nil
		}
		return &mapsurl.Resolution{
			Kind: "what3words", URL: mapsurl.BuildSearch(norm, o),
			Raw: raw, Normalized: norm, Query: norm, Confidence: 1,
		}
	})
	if got := mapsurl.Classify("///filled.count.soap"); got != "what3words" {
		t.Fatalf("kind = %q, want what3words", got)
	}
}

// TestConcurrentResolve runs the pipeline from many goroutines at once. The
// handler registry is shared global state, so this is the test that would fail
// under -race if the snapshot copy in Resolve were ever dropped.
func TestConcurrentResolve(t *testing.T) {
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			mapsurl.Get("35.95, 5.53")
		}()
	}
	wg.Wait()
}

func BenchmarkResolveAddress(b *testing.B) {
	for range b.N {
		mapsurl.Get("45 Rue Didouche Mourad, Alger")
	}
}

func BenchmarkResolveCoordinates(b *testing.B) {
	for range b.N {
		mapsurl.Get("35.95277287790832, 5.537532012437717")
	}
}
