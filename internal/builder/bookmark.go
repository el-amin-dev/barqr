package builder

import (
	"strings"
)

// Bookmark is the registry name of the MEBKM bookmark builder.
const Bookmark = "bookmark"

func init() { Register(bookmarkBuilder{}) }

// BookmarkPayload carries a titled link.
type BookmarkPayload struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// bookmarkBuilder emits NTT DoCoMo's MEBKM record.
//
// A plain URL code opens the page; a MEBKM asks the phone to save it as a
// bookmark with a title, which is the whole reason to prefer it. It shares
// MECARD's escaping rules, so the title may contain semicolons and colons.
type bookmarkBuilder struct{}

func (bookmarkBuilder) Name() string { return Bookmark }

func (bookmarkBuilder) Fields() []Field {
	return []Field{
		{
			Name: "title", Type: TypeString,
			Description: "bookmark title", Example: "Example: the menu",
		},
		{
			Name: "url", Type: TypeString, Required: true,
			Description: "the link to bookmark", Example: "https://example.com/menu",
		},
	}
}

func (b bookmarkBuilder) Build(payload any) (string, error) {
	m, err := toMap(payload)
	if err != nil {
		return "", err
	}
	if err := checkFields(m, b.Fields()); err != nil {
		return "", err
	}

	title, err := str(m, "title")
	if err != nil {
		return "", err
	}
	rawURL, err := strReq(m, "url")
	if err != nil {
		return "", err
	}
	link, err := normaliseURL(rawURL, false)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	out.WriteString("MEBKM:")
	if title != "" {
		out.WriteString("TITLE:" + docomoEscape(strings.TrimSpace(title)) + ";")
	}
	out.WriteString("URL:" + docomoEscape(link) + ";;")
	return out.String(), nil
}

func (bookmarkBuilder) Parse(raw string) (any, bool) {
	rest, ok := cutPrefixFold(raw, "MEBKM:")
	if !ok || !strings.HasSuffix(rest, ";;") {
		return nil, false
	}
	rest = strings.TrimSuffix(rest, ";")

	out := map[string]any{}
	for _, field := range docomoSplit(rest, ';') {
		if field == "" {
			continue
		}
		rawTag, rawValue, found := docomoCut(field, ':')
		if !found {
			return nil, false
		}
		switch strings.ToUpper(docomoUnescape(rawTag)) {
		case "TITLE":
			setIfNotEmpty(out, "title", docomoUnescape(rawValue))
		case "URL":
			link := docomoUnescape(rawValue)
			if !stableString(link, strictURL) {
				return nil, false
			}
			out["url"] = link
		default:
			return nil, false
		}
	}

	if _, hasURL := out["url"]; !hasURL || !trimmedValues(out) {
		return nil, false
	}
	return out, true
}
