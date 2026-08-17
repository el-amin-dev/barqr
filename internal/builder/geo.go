package builder

import (
	"fmt"
	"strconv"
	"strings"
)

// Geo is the registry name of the geographic-location builder.
const Geo = "geo"

func init() { Register(geoBuilder{}) }

// GeoPayload carries a point on the earth.
type GeoPayload struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Altitude  float64 `json:"altitude"`
	Query     string  `json:"query"`
}

// Coordinate bounds, in degrees.
const (
	latMax = 90
	lonMax = 180
)

// geoBuilder emits an RFC 5870 geo: URI.
//
// The optional "?q=" is not in RFC 5870: it is Android's extension, where the
// query becomes a search term or a pin label in Maps. iOS ignores it and drops
// a pin on the coordinates, which is the right degradation — the coordinates
// carry the meaning and the query only decorates it.
type geoBuilder struct{}

func (geoBuilder) Name() string { return Geo }

func (geoBuilder) Fields() []Field {
	return []Field{
		{
			Name: "latitude", Type: TypeNumber, Required: true,
			Description: "degrees north, -90 to 90", Example: "51.50722",
		},
		{
			Name: "longitude", Type: TypeNumber, Required: true,
			Description: "degrees east, -180 to 180", Example: "-0.1275",
		},
		{
			Name: "altitude", Type: TypeNumber,
			Description: "metres above the reference ellipsoid", Example: "11",
		},
		{
			Name: "query", Type: TypeString,
			Description: "label or search term; honoured by Android, ignored by iOS",
			Example:     "Nelson's Column",
		},
	}
}

func (b geoBuilder) Build(payload any) (string, error) {
	m, err := payloadMap(payload, b.Fields())
	if err != nil {
		return "", err
	}

	lat, ok, err := number(m, "latitude")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrMissingField, "latitude")
	}
	lon, ok, err := number(m, "longitude")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrMissingField, "longitude")
	}
	if lat < -latMax || lat > latMax {
		return "", fmt.Errorf("%w: latitude %s is outside -%d to %d",
			ErrInvalidPayload, formatNumber(lat), latMax, latMax)
	}
	if lon < -lonMax || lon > lonMax {
		return "", fmt.Errorf("%w: longitude %s is outside -%d to %d",
			ErrInvalidPayload, formatNumber(lon), lonMax, lonMax)
	}

	alt, hasAlt, err := number(m, "altitude")
	if err != nil {
		return "", err
	}
	query, err := str(m, "query")
	if err != nil {
		return "", err
	}

	out := "geo:" + formatNumber(lat) + "," + formatNumber(lon)
	if hasAlt {
		out += "," + formatNumber(alt)
	}
	if query != "" {
		out += "?q=" + pctEscape(query)
	}
	return out, nil
}

func (geoBuilder) Parse(raw string) (any, bool) {
	rest, ok := cutPrefixFold(raw, "geo:")
	if !ok {
		return nil, false
	}

	coords, query, _ := strings.Cut(rest, "?")
	// RFC 5870 allows ";crs=" and ";u=" parameters. They are not modelled, and
	// dropping them would rebuild a different URI, so they are not this
	// builder's form.
	if strings.ContainsRune(coords, ';') {
		return nil, false
	}

	parts := strings.Split(coords, ",")
	if len(parts) < 2 || len(parts) > 3 {
		return nil, false
	}
	values := make([]float64, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil || formatNumber(f) != p {
			// Rejecting a coordinate that does not survive reformatting keeps
			// the round trip exact rather than silently rewriting "51.50" as
			// "51.5".
			return nil, false
		}
		values[i] = f
	}
	if values[0] < -latMax || values[0] > latMax || values[1] < -lonMax || values[1] > lonMax {
		return nil, false
	}

	out := map[string]any{"latitude": values[0], "longitude": values[1]}
	if len(values) == 3 {
		out["altitude"] = values[2]
	}

	params, ok := parseURIQuery(query, "q")
	if !ok {
		return nil, false
	}
	if q, present := params["q"]; present {
		out["query"] = q
	}
	return out, true
}
