package builder

import (
	"fmt"
	"strings"
	"time"
)

// Event is the registry name of the calendar-event builder.
const Event = "event"

func init() { Register(eventBuilder{}) }

// EventPayload carries a calendar event.
type EventPayload struct {
	Summary     string `json:"summary"`
	Start       string `json:"start"`
	End         string `json:"end"`
	Location    string `json:"location"`
	Description string `json:"description"`
}

// icalTimeLayout is RFC 5545's UTC date-time form. The trailing Z is what
// makes it unambiguous; a floating local time in a printed code means a
// different moment to everyone who scans it.
const icalTimeLayout = "20060102T150405Z"

// icalProdID identifies the generator, which RFC 5545 requires on a VCALENDAR.
const icalProdID = "-//barqr//barqr//EN"

// eventBuilder emits an iCalendar VEVENT.
//
// The VEVENT is wrapped in a VCALENDAR because a bare component is not a valid
// iCalendar object and desktop calendar apps refuse to import one; scanner
// apps look for BEGIN:VEVENT anywhere in the payload, so the wrapper costs a
// few dozen modules and loses nothing.
//
// UID and DTSTAMP are deliberately absent. Both would have to come from a
// clock or a random source, which would break the rule that Build is a pure
// function of its payload, and every importer assigns its own anyway.
type eventBuilder struct{}

func (eventBuilder) Name() string { return Event }

func (eventBuilder) Fields() []Field {
	return []Field{
		{
			Name: "summary", Type: TypeString, Required: true,
			Description: "event title", Example: "Ada's birthday drinks",
		},
		{
			Name: "start", Type: TypeString, Required: true,
			Description: "start instant, RFC 3339 or 20060102T150405Z; converted to UTC",
			Example:     "2026-12-10T18:30:00Z",
		},
		{
			Name: "end", Type: TypeString,
			Description: "end instant, same formats as start; must not precede it",
			Example:     "2026-12-10T21:00:00Z",
		},
		{
			Name: "location", Type: TypeString,
			Description: "where it happens", Example: "The Engine Room, London",
		},
		{
			Name: "description", Type: TypeString,
			Description: "longer details", Example: "Bring cake.",
		},
	}
}

func (b eventBuilder) Build(payload any) (string, error) {
	m, err := payloadMap(payload, b.Fields())
	if err != nil {
		return "", err
	}

	summary, err := strReq(m, "summary")
	if err != nil {
		return "", err
	}
	// Trimmed like every other text field here, so that Parse can insist on
	// the trimmed form and still recover what Build produced.
	summary = strings.TrimSpace(summary)
	rawStart, err := strReq(m, "start")
	if err != nil {
		return "", err
	}
	start, err := parseEventTime("start", rawStart)
	if err != nil {
		return "", err
	}

	v, err := readStrings(m, []string{"end", "location", "description"})
	if err != nil {
		return "", err
	}

	end := time.Time{}
	if v["end"] != "" {
		end, err = parseEventTime("end", v["end"])
		if err != nil {
			return "", err
		}
		if end.Before(start) {
			return "", fmt.Errorf("%w: end %s precedes start %s",
				ErrInvalidPayload, end.Format(icalTimeLayout), start.Format(icalTimeLayout))
		}
	}

	var out strings.Builder
	writeLine := func(name, value string) {
		if value == "" {
			return
		}
		out.WriteString(name + ":" + value + crlf)
	}

	out.WriteString("BEGIN:VCALENDAR" + crlf)
	out.WriteString("VERSION:2.0" + crlf)
	out.WriteString("PRODID:" + icalProdID + crlf)
	out.WriteString("BEGIN:VEVENT" + crlf)
	writeLine("SUMMARY", escapeTextValue(summary))
	writeLine("DTSTART", start.Format(icalTimeLayout))
	if !end.IsZero() {
		writeLine("DTEND", end.Format(icalTimeLayout))
	}
	writeLine("LOCATION", escapeTextValue(v["location"]))
	writeLine("DESCRIPTION", escapeTextValue(v["description"]))
	out.WriteString("END:VEVENT" + crlf)
	out.WriteString("END:VCALENDAR" + crlf)

	return out.String(), nil
}

func (eventBuilder) Parse(raw string) (any, bool) {
	lines, ok := splitContentLines(raw, "VCALENDAR")
	if !ok {
		// A bare VEVENT is common in codes from other generators, so it is
		// accepted on the way in even though it is never produced.
		lines, ok = splitContentLines(raw, "VEVENT")
		if !ok {
			return nil, false
		}
	} else {
		if len(lines) < 3 || !strings.EqualFold(lines[len(lines)-1], "END:VEVENT") {
			return nil, false
		}
		begin := -1
		for i, line := range lines {
			if strings.EqualFold(line, "BEGIN:VEVENT") {
				begin = i
				break
			}
		}
		if begin < 0 {
			return nil, false
		}
		lines = lines[begin+1 : len(lines)-1]
	}

	out := map[string]any{}
	for _, line := range lines {
		name, _, value, lineOK := splitContentLine(line)
		if !lineOK {
			return nil, false
		}
		switch name {
		case "VERSION", "PRODID", "UID", "DTSTAMP":
			// Envelope and bookkeeping properties carry nothing the payload
			// models; they are skipped rather than treated as foreign.
		case "SUMMARY":
			setIfNotEmpty(out, "summary", unescapeTextValue(value))
		case "LOCATION":
			setIfNotEmpty(out, "location", unescapeTextValue(value))
		case "DESCRIPTION":
			setIfNotEmpty(out, "description", unescapeTextValue(value))
		case "DTSTART", "DTEND":
			if _, err := time.Parse(icalTimeLayout, value); err != nil {
				return nil, false
			}
			key := "start"
			if name == "DTEND" {
				key = "end"
			}
			out[key] = value
		default:
			return nil, false
		}
	}

	if _, hasStart := out["start"]; !hasStart {
		return nil, false
	}
	if _, hasSummary := out["summary"]; !hasSummary {
		return nil, false
	}
	if !trimmedValues(out) || !noRawCarriageReturn(out) {
		return nil, false
	}
	// The same ordering rule Build applies. Both values parsed cleanly above,
	// so the layout cannot fail here.
	if end, hasEnd := out["end"].(string); hasEnd {
		start, _ := out["start"].(string)
		startAt, _ := time.Parse(icalTimeLayout, start)
		endAt, _ := time.Parse(icalTimeLayout, end)
		if endAt.Before(startAt) {
			return nil, false
		}
	}
	return out, true
}

// parseEventTime accepts the two spellings a caller is likely to have to hand:
// RFC 3339, which is what a JSON API produces, and the basic UTC form, which
// is what this builder emits and therefore what Parse hands back.
func parseEventTime(field, value string) (time.Time, error) {
	for _, layout := range []string{icalTimeLayout, time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf(
		"%w: field %q: expected an instant like 2026-12-10T18:30:00Z, got %q",
		ErrInvalidPayload, field, value)
}
