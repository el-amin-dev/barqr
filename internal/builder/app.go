package builder

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// App is the registry name of the app-store-link builder.
const App = "app"

func init() { Register(appBuilder{}) }

// AppPayload carries a link to an application listing.
type AppPayload struct {
	Platform       string `json:"platform"`
	IOSID          string `json:"ios_id"`
	AndroidPackage string `json:"android_package"`
}

// Store URL shapes.
const (
	appStoreURL  = "https://apps.apple.com/app/id"
	playStoreURL = "https://play.google.com/store/apps/details?id="
	intentPrefix = "intent://details?id="
	intentSuffix = ";end"
)

var (
	// appIOSID is the numeric App Store identifier, with or without the "id"
	// prefix a caller may have copied from the store URL.
	appIOSID = regexp.MustCompile(`^[0-9]{5,12}$`)
	// appAndroidPackage is a Java package name, which is what Play uses as an
	// application id.
	appAndroidPackage = regexp.MustCompile(
		`^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z0-9_][a-zA-Z0-9_]*)+$`)
)

// appBuilder emits a store link for one or both mobile platforms.
//
// A printed code cannot branch on the operating system, so "both" needs a
// single URL that behaves differently on each. The Android intent: URL is that
// URL: Chrome on Android resolves it to the Play Store listing through the
// market scheme, and everything else — iOS, desktop browsers, and any scanner
// that hands the string to a browser it does not control — follows the
// S.browser_fallback_url to the App Store page.
//
// The honest caveat: a scanner app on iOS that refuses to open an unknown
// scheme outright will show the raw intent: string rather than the fallback.
// There is no single-URL scheme that avoids this; the alternative is a
// redirector on a host you own, which this package cannot assume exists.
type appBuilder struct{}

func (appBuilder) Name() string { return App }

func (appBuilder) Fields() []Field {
	return []Field{
		{
			Name: "platform", Type: TypeString, Required: true,
			Description: "ios, android, or both", Example: "both",
		},
		{
			Name: "ios_id", Type: TypeString,
			Description: "numeric App Store id, required for ios and both",
			Example:     "310633997",
		},
		{
			Name: "android_package", Type: TypeString,
			Description: "Play application id, required for android and both",
			Example:     "com.example.app",
		},
	}
}

func (b appBuilder) Build(payload any) (string, error) {
	m, err := payloadMap(payload, b.Fields())
	if err != nil {
		return "", err
	}

	v, err := readStrings(m, fieldNames(b.Fields()))
	if err != nil {
		return "", err
	}

	platform := strings.ToLower(v["platform"])
	if platform == "" {
		return "", fmt.Errorf("%w: %q", ErrMissingField, "platform")
	}
	if platform != "ios" && platform != "android" && platform != "both" {
		return "", fmt.Errorf("%w: platform %q: expected ios, android or both",
			ErrInvalidPayload, v["platform"])
	}

	iosID := strings.TrimPrefix(strings.ToLower(v["ios_id"]), "id")
	if platform != "android" {
		if iosID == "" {
			return "", fmt.Errorf("%w: %q is required for platform %s",
				ErrMissingField, "ios_id", platform)
		}
		if !appIOSID.MatchString(iosID) {
			return "", fmt.Errorf("%w: ios_id %q: expected the numeric App Store id",
				ErrInvalidPayload, v["ios_id"])
		}
	} else if iosID != "" {
		return "", fmt.Errorf("%w: ios_id is meaningless for platform android",
			ErrInvalidPayload)
	}

	pkg := v["android_package"]
	if platform != "ios" {
		if pkg == "" {
			return "", fmt.Errorf("%w: %q is required for platform %s",
				ErrMissingField, "android_package", platform)
		}
		if !appAndroidPackage.MatchString(pkg) {
			return "", fmt.Errorf("%w: android_package %q: expected a package name "+
				"such as com.example.app", ErrInvalidPayload, pkg)
		}
	} else if pkg != "" {
		return "", fmt.Errorf("%w: android_package is meaningless for platform ios",
			ErrInvalidPayload)
	}

	switch platform {
	case "ios":
		return appStoreURL + iosID, nil
	case "android":
		return playStoreURL + pkg, nil
	default:
		return intentPrefix + pkg +
			"#Intent;scheme=market;package=com.android.vending;S.browser_fallback_url=" +
			pctEscape(appStoreURL+iosID) + intentSuffix, nil
	}
}

func (appBuilder) Parse(raw string) (any, bool) {
	if id, ok := strings.CutPrefix(raw, appStoreURL); ok {
		if !appIOSID.MatchString(id) {
			return nil, false
		}
		return map[string]any{"platform": "ios", "ios_id": id}, true
	}
	if pkg, ok := strings.CutPrefix(raw, playStoreURL); ok {
		if !appAndroidPackage.MatchString(pkg) {
			return nil, false
		}
		return map[string]any{"platform": "android", "android_package": pkg}, true
	}

	rest, ok := strings.CutPrefix(raw, intentPrefix)
	if !ok {
		return nil, false
	}
	pkg, fragment, found := strings.Cut(rest, "#Intent;")
	if !found || !appAndroidPackage.MatchString(pkg) {
		return nil, false
	}
	fragment, ok = strings.CutSuffix(fragment, intentSuffix)
	if !ok {
		return nil, false
	}

	fallback := ""
	for _, extra := range strings.Split(fragment, ";") {
		key, value, hasValue := strings.Cut(extra, "=")
		if !hasValue {
			return nil, false
		}
		switch key {
		// Both are fixed on the way out; a different value means a different
		// generator's intent URL, which this builder cannot rebuild.
		case "scheme":
			if value != "market" {
				return nil, false
			}
		case "package":
			if value != "com.android.vending" {
				return nil, false
			}
		case "S.browser_fallback_url":
			decoded, err := url.PathUnescape(value)
			if err != nil {
				return nil, false
			}
			fallback = decoded
		default:
			return nil, false
		}
	}

	iosID, ok := strings.CutPrefix(fallback, appStoreURL)
	if !ok || !appIOSID.MatchString(iosID) {
		return nil, false
	}
	return map[string]any{"platform": "both", "ios_id": iosID, "android_package": pkg}, true
}
