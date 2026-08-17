package batch

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseCSVMapsColumnsOntoItems(t *testing.T) {
	t.Parallel()

	text := "id,data,style.module,output.format,encode.ecc\n" +
		"a,https://example.com,dot,svg,H\n" +
		"b,hello,,,\n"

	items, err := ParseCSV(text)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	first := items[0]
	if first.ID != "a" || first.Data != "https://example.com" {
		t.Fatalf("first item = %+v", first)
	}
	want := map[string]any{"style.module": "dot", "output.format": "svg", "encode.ecc": "H"}
	if fmt.Sprint(first.Options) != fmt.Sprint(want) {
		t.Fatalf("first options = %v, want %v", first.Options, want)
	}

	// A blank cell means "not set", so the second item carries no options at
	// all rather than three empty strings that would override the defaults.
	if items[1].Options != nil {
		t.Fatalf("second options = %v, want none", items[1].Options)
	}
}

func TestParseCSVBuildsPayloadsWithoutADataColumn(t *testing.T) {
	t.Parallel()

	text := "type,payload.name,payload.email\n" +
		"vcard,Ada Lovelace,ada@example.com\n"

	items, err := ParseCSV(text)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(items) != 1 || items[0].Type != "vcard" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Payload["name"] != "Ada Lovelace" || items[0].Payload["email"] != "ada@example.com" {
		t.Fatalf("payload = %v", items[0].Payload)
	}
	// A payload column must not leak into the option namespace, where the
	// request layer would reject it as an unknown field.
	if items[0].Options != nil {
		t.Fatalf("options = %v, want none", items[0].Options)
	}
}

func TestParseCSVNormalisesTheHeader(t *testing.T) {
	t.Parallel()

	// Uppercase, padded, and preceded by the BOM a spreadsheet export writes.
	text := "\ufeff ID , Data , Style.Module \nx,hello,dot\n"

	items, err := ParseCSV(text)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(items) != 1 || items[0].ID != "x" || items[0].Data != "hello" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Options["style.module"] != "dot" {
		t.Fatalf("options = %v", items[0].Options)
	}
}

func TestParseCSVSkipsFillerRows(t *testing.T) {
	t.Parallel()

	// The trailing comma row is what Excel leaves at the end of an export.
	items, err := ParseCSV("id,data\na,one\n,\nb,two\n,\n")
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(items) != 2 || items[0].ID != "a" || items[1].ID != "b" {
		t.Fatalf("items = %+v", items)
	}
}

func TestParseCSVPreservesLeadingSpaceInData(t *testing.T) {
	t.Parallel()

	items, err := ParseCSV("data\n\"  padded\"\n")
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if items[0].Data != "  padded" {
		t.Fatalf("data = %q, want the leading space kept", items[0].Data)
	}
}

func TestParseCSVRejectsBadDocuments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		text  string
		wants string
	}{
		{"empty", "", "the document is empty"},
		{"whitespace only", "   \n  ", "the document is empty"},
		{"no data column", "id,style.module\na,dot\n", "no data column"},
		{"type without payload", "id,type\na,vcard\n", "no data column"},
		{"unknown column", "data,colour\nx,red\n", `unknown column "colour"`},
		{"duplicate column", "data,data\nx,y\n", `column "data" appears twice`},
		{"bare option prefix", "data,style.\nx,y\n", "names no option"},
		{"bare payload prefix", "type,payload.\nvcard,y\n", "names no payload field"},
		{"unterminated quote", "data\n\"open\n", "quote"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseCSV(tc.text)
			if !errors.Is(err, ErrBadCSV) {
				t.Fatalf("err = %v, want ErrBadCSV", err)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("err = %q, want it to mention %q", err, tc.wants)
			}
		})
	}
}

func TestParseCSVNamesTheOffendingRow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		text    string
		wantRow int
		wants   string
	}{
		{
			"ragged row",
			"id,data\na,one\nb\nc,three\n",
			3,
			"wrong number of columns",
		},
		{
			"neither data nor type",
			"id,data\na,one\nb,\nc,three\n",
			3,
			"no data and no type",
		},
		{
			"row after a quoted field spanning lines",
			"id,data\na,\"line one\nline two\"\nb,\n",
			4,
			"no data and no type",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseCSV(tc.text)
			if err == nil {
				t.Fatal("want an error")
			}
			want := fmt.Sprintf("row %d", tc.wantRow)
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %q, want it to name %q", err, want)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("err = %q, want it to mention %q", err, tc.wants)
			}
		})
	}
}

func TestParseCSVFindsTheBadRowInALongDocument(t *testing.T) {
	t.Parallel()

	// The whole point of the row number: a nine-hundred-row upload that fails
	// once is undebuggable without it.
	var b strings.Builder
	b.WriteString("id,data\n")
	const badRow = 903 // header is row 1, so this is the 902nd data row
	for i := 2; i <= 1000; i++ {
		if i == badRow {
			fmt.Fprintf(&b, "id-%d,\n", i)
			continue
		}
		fmt.Fprintf(&b, "id-%d,payload-%d\n", i, i)
	}

	_, err := ParseCSV(b.String())
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "row 903") {
		t.Fatalf("err = %q, want it to name row 903", err)
	}
}

func TestParseCSVAcceptsAHeaderWithNoRows(t *testing.T) {
	t.Parallel()

	// Parsing succeeds; it is Run that decides an empty batch is a fault, so
	// that a caller assembling items itself gets one consistent error.
	items, err := ParseCSV("id,data\n")
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %+v, want none", items)
	}
}

func TestParseCSVIgnoresUnnamedColumns(t *testing.T) {
	t.Parallel()

	items, err := ParseCSV("data,\nhello,junk\n")
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(items) != 1 || items[0].Data != "hello" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Options != nil {
		t.Fatalf("options = %v, want none", items[0].Options)
	}
}

func TestParseCSVToleratesAShortFinalRecord(t *testing.T) {
	t.Parallel()

	// csv itself rejects a ragged row, so buildItem's bounds check is defence
	// against a future reader configured with FieldsPerRecord = -1.
	cols := []column{{name: "data", kind: kindData}, {name: "id", kind: kindID}}
	item, empty := buildItem(cols, []string{"only-data"})
	if empty || item.Data != "only-data" || item.ID != "" {
		t.Fatalf("item = %+v, empty = %v", item, empty)
	}
}
