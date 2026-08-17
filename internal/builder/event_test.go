package builder

import (
	"strings"
	"testing"
)

// eventHeader is the fixed envelope every built event carries.
const eventHeader = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:" + icalProdID + "\r\nBEGIN:VEVENT\r\n"

// eventFooter closes it.
const eventFooter = "END:VEVENT\r\nEND:VCALENDAR\r\n"

func TestEventBuild(t *testing.T) {
	t.Parallel()

	b := eventBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name: "a summary and a start are enough",
			payload: map[string]any{
				"summary": "Ada's birthday drinks", "start": "2026-12-10T18:30:00Z",
			},
			want: eventHeader + "SUMMARY:Ada's birthday drinks\r\n" +
				"DTSTART:20261210T183000Z\r\n" + eventFooter,
		},
		{
			name: "an offset start is converted to UTC",
			payload: map[string]any{
				"summary": "Standup", "start": "2026-12-10T09:00:00+01:00",
			},
			want: eventHeader + "SUMMARY:Standup\r\nDTSTART:20261210T080000Z\r\n" + eventFooter,
		},
		{
			name: "a comma in a location is escaped",
			payload: map[string]any{
				"summary": "Drinks", "start": "20261210T183000Z",
				"end": "20261210T210000Z", "location": "The Engine Room, London",
			},
			want: eventHeader + "SUMMARY:Drinks\r\nDTSTART:20261210T183000Z\r\n" +
				"DTEND:20261210T210000Z\r\nLOCATION:The Engine Room\\, London\r\n" + eventFooter,
		},
		{
			name: "an end before the start is rejected",
			payload: map[string]any{
				"summary": "Time travel", "start": "20261210T183000Z", "end": "20261210T180000Z",
			},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unparseable instant is rejected",
			payload: map[string]any{"summary": "When?", "start": "next Tuesday"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing summary is rejected",
			payload: map[string]any{"start": "20261210T183000Z"},
			wantErr: ErrMissingField,
		},
		{
			name:    "a missing start is rejected",
			payload: map[string]any{"summary": "When?"},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"summary": "x", "start": "20261210T183000Z", "summry": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestEventEscapesHostileText(t *testing.T) {
	t.Parallel()

	b := eventBuilder{}
	for _, field := range []string{"summary", "location", "description"} {
		payload := map[string]any{"summary": "Drinks", "start": "20261210T183000Z"}
		payload[field] = hostileInput
		assertHostileRoundTrip(t, b, payload, field)
	}

	raw, err := b.Build(map[string]any{
		"summary": "Drinks", "start": "20261210T183000Z", "description": hostileInput,
	})
	if err != nil {
		t.Fatalf("Build = error %v", err)
	}
	if strings.Count(raw, "\n") != strings.Count(raw, "\r\n") {
		t.Errorf("a raw newline leaked into %q", raw)
	}
}

func TestEventParse(t *testing.T) {
	t.Parallel()

	b := eventBuilder{}
	// A bare VEVENT is common from other generators and is accepted even
	// though it is never produced.
	assertParsed(t, b,
		"BEGIN:VEVENT\r\nSUMMARY:Drinks\r\nDTSTART:20261210T183000Z\r\nEND:VEVENT\r\n",
		map[string]any{"summary": "Drinks", "start": "20261210T183000Z"})

	assertNotParsed(t, b,
		// No start.
		"BEGIN:VEVENT\r\nSUMMARY:Drinks\r\nEND:VEVENT\r\n",
		// No summary.
		"BEGIN:VEVENT\r\nDTSTART:20261210T183000Z\r\nEND:VEVENT\r\n",
		// A floating local time, which means a different moment to everyone.
		"BEGIN:VEVENT\r\nSUMMARY:x\r\nDTSTART:20261210T183000\r\nEND:VEVENT\r\n",
		// A property that would be lost on a rebuild.
		"BEGIN:VEVENT\r\nSUMMARY:x\r\nDTSTART:20261210T183000Z\r\nRRULE:FREQ=DAILY\r\nEND:VEVENT\r\n",
		// An end before the start.
		"BEGIN:VEVENT\r\nSUMMARY:x\r\nDTSTART:20261210T183000Z\r\nDTEND:20261210T180000Z\r\n"+
			"END:VEVENT\r\n",
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n",
		"not an event",
	)
}
