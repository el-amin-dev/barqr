package builder

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// foreignString is a payload that belongs to no builder: it opens with a
// prefix the text builder screens out, and it is malformed for the mecard
// builder whose prefix it borrows.
const foreignString = "MECARD:not a real payload at all"

// catchAllBuilders parse anything by design, so the "reject a foreign string"
// invariant cannot apply to them. Keeping the exemption explicit means a new
// builder cannot join the list by accident.
var catchAllBuilders = map[string]bool{Raw: true}

// examplePayload builds the payload a builder documents for itself. Fields
// whose Example is empty are omitted: those are the ones that conflict with
// another field's example, such as an hotp counter next to a totp type.
func examplePayload(b Builder) map[string]any {
	out := map[string]any{}
	for _, f := range b.Fields() {
		if f.Example != "" {
			out[f.Name] = f.Example
		}
	}
	return out
}

// hostileInput carries every character the formats in this package give
// meaning to — semicolon, comma, backslash, colon, a line break — plus a
// non-ASCII character, so that an escaping bug and an encoding bug both show
// up as the same failed round trip.
const hostileInput = "a;b,c\\d:e\nf é☃"

// buildCase is one row of a builder's table test. Exactly one of want and
// wantErr is meaningful: a case either produces a payload or a failure.
type buildCase struct {
	name    string
	payload map[string]any
	want    string
	wantErr error
}

// runBuildCases drives a builder's table. Every successful case is also held
// to the round-trip invariant, so a table row doubles as a parser test.
func runBuildCases(t *testing.T, b Builder, cases []buildCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := b.Build(tc.payload)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Build = %q, %v; want error %v", got, err, tc.wantErr)
				}
				if got != "" {
					t.Errorf("Build returned %q alongside an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Build = error %v, want %q", err, tc.want)
			}
			if got != tc.want {
				t.Fatalf("Build = %q, want %q", got, tc.want)
			}

			parsed, ok := b.Parse(got)
			if !ok {
				t.Fatalf("Parse(%q) rejected a string this builder built", got)
			}
			rebuilt, err := b.Build(parsed)
			if err != nil {
				t.Fatalf("rebuilding the parsed payload = error %v", err)
			}
			if rebuilt != got {
				t.Errorf("round trip changed the payload:\n built %q\nrebuilt %q", got, rebuilt)
			}
		})
	}
}

// assertHostileRoundTrip checks that a value full of the format's own
// delimiters survives a build and a parse byte for byte. Comparing the
// recovered value is the only honest test of an escaping implementation:
// asserting on the escaped bytes would just restate the code.
func assertHostileRoundTrip(t *testing.T, b Builder, payload map[string]any, field string) {
	t.Helper()

	raw, err := b.Build(payload)
	if err != nil {
		t.Fatalf("Build with a hostile %s = error %v", field, err)
	}
	parsed, ok := b.Parse(raw)
	if !ok {
		t.Fatalf("Parse(%q) rejected a string this builder built", raw)
	}
	m, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("Parse returned %T, want map[string]any", parsed)
	}
	if m[field] != payload[field] {
		t.Errorf("%s did not survive escaping:\n sent %q\n back %q", field, payload[field], m[field])
	}
}

// assertParsed checks Parse on a string this package did not necessarily
// build, which is how tolerance for other generators' output is pinned down.
func assertParsed(t *testing.T, b Builder, raw string, want map[string]any) {
	t.Helper()

	parsed, ok := b.Parse(raw)
	if !ok {
		t.Fatalf("Parse(%q) = not-my-form, want %v", raw, want)
	}
	if !reflect.DeepEqual(parsed, any(want)) {
		t.Errorf("Parse(%q) = %#v, want %#v", raw, parsed, want)
	}
}

// assertNotParsed checks that a string is rejected as foreign.
func assertNotParsed(t *testing.T, b Builder, raws ...string) {
	t.Helper()

	for _, raw := range raws {
		if payload, ok := b.Parse(raw); ok {
			t.Errorf("Parse(%q) = %v, true; want not-my-form", raw, payload)
		}
	}
}

// TestEveryBuilderRoundTripsItsExamples is the test that catches a new builder
// added without a working Parse. It uses each builder's own documented example
// values, so it cannot go stale as fields are added.
func TestEveryBuilderRoundTripsItsExamples(t *testing.T) {
	t.Parallel()

	for _, b := range All() {
		t.Run(b.Name(), func(t *testing.T) {
			t.Parallel()

			payload := examplePayload(b)
			raw, err := b.Build(payload)
			if err != nil {
				t.Fatalf("Build(%v) = error %v, want a payload", payload, err)
			}
			if raw == "" {
				t.Fatal("Build returned an empty string")
			}

			parsed, ok := b.Parse(raw)
			if !ok {
				t.Fatalf("Parse(%q) reported not-my-form for a string this builder built", raw)
			}
			if _, isMap := parsed.(map[string]any); !isMap {
				t.Fatalf("Parse returned %T, want map[string]any", parsed)
			}

			rebuilt, err := b.Build(parsed)
			if err != nil {
				t.Fatalf("Build(Parse(Build(p))) = error %v", err)
			}
			if rebuilt != raw {
				t.Errorf("round trip changed the payload:\n built %q\nrebuilt %q", raw, rebuilt)
			}

			// Parsing the rebuilt string must give the identical payload, which
			// is the "recovers an equal payload" half of the invariant.
			reparsed, ok := b.Parse(rebuilt)
			if !ok {
				t.Fatal("Parse rejected its own rebuilt string")
			}
			if !reflect.DeepEqual(parsed, reparsed) {
				t.Errorf("Parse is not stable: %#v then %#v", parsed, reparsed)
			}
		})
	}
}

// TestEveryBuilderRejectsAForeignString guards the classification use of
// Parse: a caller tries every builder in turn, so a builder that claims a
// string it cannot rebuild would misroute it.
func TestEveryBuilderRejectsAForeignString(t *testing.T) {
	t.Parallel()

	for _, b := range All() {
		if catchAllBuilders[b.Name()] {
			continue
		}
		t.Run(b.Name(), func(t *testing.T) {
			t.Parallel()

			if payload, ok := b.Parse(foreignString); ok {
				t.Errorf("Parse(%q) = %v, true; want not-my-form", foreignString, payload)
			}
		})
	}
}

// TestEveryBuilderRejectsAnUnknownField checks that the shared payload helper
// is actually wired into every builder, suggestion and all.
func TestEveryBuilderRejectsAnUnknownField(t *testing.T) {
	t.Parallel()

	for _, b := range All() {
		t.Run(b.Name(), func(t *testing.T) {
			t.Parallel()

			payload := examplePayload(b)
			// A near-miss of the first declared field, so the suggestion has
			// something to find.
			typo := b.Fields()[0].Name + "x"
			payload[typo] = "anything"

			_, err := b.Build(payload)
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("Build with unknown field %q = %v, want ErrInvalidPayload", typo, err)
			}
			if !strings.Contains(err.Error(), typo) {
				t.Errorf("error %q does not name the offending field %q", err, typo)
			}
			if !strings.Contains(err.Error(), b.Fields()[0].Name) {
				t.Errorf("error %q does not suggest the closest field %q", err, b.Fields()[0].Name)
			}
		})
	}
}

// TestEveryBuilderDeclaresUsableFields keeps the documentation honest: the
// examples are what the round-trip test builds from, so a required field
// without one would quietly stop being tested.
func TestEveryBuilderDeclaresUsableFields(t *testing.T) {
	t.Parallel()

	for _, b := range All() {
		t.Run(b.Name(), func(t *testing.T) {
			t.Parallel()

			fields := b.Fields()
			if len(fields) == 0 {
				t.Fatal("Fields is empty")
			}

			seen := map[string]bool{}
			for _, f := range fields {
				switch {
				case f.Name == "":
					t.Error("a field has no name")
				case seen[f.Name]:
					t.Errorf("field %q is declared twice", f.Name)
				case f.Description == "":
					t.Errorf("field %q has no description", f.Name)
				case f.Type != TypeString && f.Type != TypeBool && f.Type != TypeNumber:
					t.Errorf("field %q has type %q", f.Name, f.Type)
				case f.Required && f.Example == "":
					t.Errorf("required field %q has no example", f.Name)
				}
				seen[f.Name] = true
			}
		})
	}
}

// TestEveryBuilderNameMatchesItsRegistration catches a copy-pasted builder
// that kept the name of the file it was copied from.
func TestEveryBuilderNameMatchesItsRegistration(t *testing.T) {
	t.Parallel()

	names := Names()
	if len(names) != len(All()) {
		t.Fatalf("Names has %d entries, All has %d", len(names), len(All()))
	}

	for _, name := range names {
		b, err := Get(name)
		if err != nil {
			t.Fatalf("Get(%q) = error %v", name, err)
		}
		if b.Name() != name {
			t.Errorf("builder registered as %q reports name %q", name, b.Name())
		}
	}
}

func TestGetRejectsAnUnknownType(t *testing.T) {
	t.Parallel()

	if _, err := Get("no-such-type"); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("Get on an unregistered name = %v, want ErrUnknownType", err)
	}
}

func TestRegisterPanicsOnDuplicate(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("Register of an already-registered name did not panic")
		}
	}()
	// Re-registering an existing builder panics before it mutates the map, so
	// the shared registry survives this test intact.
	Register(textBuilder{})
}

func TestRegisterPanicsOnEmptyName(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("Register with an empty name did not panic")
		}
	}()
	Register(namelessBuilder{})
}

// namelessBuilder is a programming error made concrete for the registry test.
type namelessBuilder struct{}

func (namelessBuilder) Name() string              { return "" }
func (namelessBuilder) Fields() []Field           { return nil }
func (namelessBuilder) Build(any) (string, error) { return "", nil }
func (namelessBuilder) Parse(string) (any, bool)  { return nil, false }

// FuzzParse drives every builder's Parse over arbitrary input. Parse is the
// one entry point that sees strings from outside — a scanned code, a pasted
// payload — so a panic in it is a denial of service. Any string Parse does
// claim must also survive a rebuild, since a caller that classifies a code
// immediately re-encodes it.
func FuzzParse(f *testing.F) {
	for _, b := range All() {
		if raw, err := b.Build(examplePayload(b)); err == nil {
			f.Add(raw)
		}
	}
	for _, seed := range []string{
		"", "\x00", "\\", ":", ";;", "MECARD:;;", "WIFI:;;", "geo:,",
		"BEGIN:VCARD\r\nEND:VCARD\r\n", "otpauth://totp/?secret=", "bitcoin:?amount=",
		strings.Repeat("a", 400), "BCD\n002\n1\nSCT\n\n\n\n",
	} {
		f.Add(seed)
	}
	// Regressions this target found. Each one broke the round trip in a
	// different way, and each is cheap insurance against the same mistake
	// coming back in a new builder.
	for _, seed := range []string{
		" ",                     // whitespace is content to raw and text
		"MEBKM:;URL:0;;",        // a link that Build would have normalised
		"WIFI:S:00;;",           // no T segment: Parse has to fill the default in
		"WIFI:S: ;;",            // an ssid of nothing but spaces, which Build refuses
		"mAilto:0@0.0?suBjeCt=", // an empty parameter is not the same as an absent one
		"MECARD:N:, ;;",         // a name that Build would have trimmed away
		"https://wA.me/000(",    // a number that Build would have stripped
		"BCD\n002\n1\nSCT\n0\n0\nDE89370400440532013000", // a malformed BIC
		"otpauth://totp/a?secret=A&digits=0",             // digits out of range
		"BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nSUMMARY:0\r\n" + // a bare CR inside
			"DTSTART:20270110T000000Z\r\nLOCATION:0\r0\r\n" + // a value, which
			"END:VEVENT\r\nEND:VCALENDAR", // escaping folds to LF
		"BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nSUMMARY:0\r\nDTSTART:20270110T000000Z\r\n" +
			"DTEND:00000110T000000Z\r\nEND:VEVENT\r\nEND:VCALENDAR", // end before start
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		for _, b := range All() {
			payload, ok := b.Parse(raw)
			if !ok {
				continue
			}
			if _, isMap := payload.(map[string]any); !isMap {
				t.Fatalf("%s: Parse(%q) returned %T, want map[string]any", b.Name(), raw, payload)
			}
			rebuilt, err := b.Build(payload)
			if err != nil {
				t.Fatalf("%s: Parse accepted %q but Build rejected the payload: %v",
					b.Name(), raw, err)
			}
			if again, ok := b.Parse(rebuilt); !ok || !reflect.DeepEqual(payload, again) {
				t.Fatalf("%s: unstable round trip for %q: %#v then %#v (ok=%t)",
					b.Name(), raw, payload, again, ok)
			}
		}
	})
}

// phoneBuilders are the builders that carry a telephone number, and so share
// normalisePhoneRegion and the phone_region field.
func phoneBuilders() map[string]Builder {
	return map[string]Builder{
		Tel:      telBuilder{},
		WhatsApp: whatsappBuilder{},
		VCard:    vcardBuilder{},
		MeCard:   mecardBuilder{},
	}
}

// phonePayload is a minimal valid payload for a phone-carrying builder: the
// number, plus whatever else that builder requires.
func phonePayload(name, phone, region string) map[string]any {
	m := map[string]any{"phone": phone}
	if region != "" {
		m["phone_region"] = region
	}
	switch name {
	case VCard, MeCard:
		m["last_name"] = "Example"
	}
	return m
}

// TestParseNeverReportsAPhoneRegion pins the resolution of the round-trip
// problem that phone_region creates.
//
// Build turns a national number and a region into E.164; Parse sees only the
// E.164 and cannot recover the pair. It must therefore not claim to — the same
// reason vcard's Parse never recovers FN. Without this, someone helpfully
// adding region recovery later would break Build(Parse(x)) == x in a way only
// the fuzzer would find, and only sometimes.
func TestParseNeverReportsAPhoneRegion(t *testing.T) {
	t.Parallel()

	for name, b := range phoneBuilders() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			built, err := b.Build(phonePayload(name, "0664108852", "DZ"))
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			parsed, ok := b.Parse(built)
			if !ok {
				t.Fatalf("Parse(%q) did not recognise its own output", built)
			}
			m, ok := parsed.(map[string]any)
			if !ok {
				t.Fatalf("Parse returned %T, want map[string]any", parsed)
			}
			if v, present := m["phone_region"]; present {
				t.Errorf("Parse reported phone_region=%v; the built string "+
					"carries no region, so claiming one breaks the round trip", v)
			}

			// And the round trip itself still closes.
			rebuilt, err := b.Build(m)
			if err != nil {
				t.Fatalf("rebuild: %v", err)
			}
			if rebuilt != built {
				t.Errorf("rebuild = %q, want %q", rebuilt, built)
			}
		})
	}
}

// TestPhoneRegionIsBackwardsCompatible is the compatibility promise as an
// executable assertion: a caller who passes a national number and no region
// gets exactly what v0.1.0 gave them.
//
// barqr cannot know that a national number is wrong — a code printed for
// domestic use is legitimate, and short codes are national by definition — and
// it has no locale from which to guess a region. Rejecting or defaulting would
// turn a visibly local number into an invisibly wrong one. See ADR-017.
func TestPhoneRegionIsBackwardsCompatible(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		Tel:      "tel:0664108852",
		WhatsApp: "https://wa.me/0664108852",
		MeCard:   "TEL:0664108852;",
		VCard:    "TEL;TYPE=WORK,VOICE:0664108852",
	}

	for name, b := range phoneBuilders() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := b.Build(phonePayload(name, "0664108852", ""))
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if !strings.Contains(got, want[name]) {
				t.Errorf("Build = %q, want it to contain %q — a national "+
					"number with no region must be unchanged", got, want[name])
			}
		})
	}
}
