package batch

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The bare column names a header may use.
const (
	colID   = "id"
	colData = "data"
	colType = "type"
)

// optionPrefixes are the dot-notation namespaces a column may address. They
// are the request layer's own sections, so a CSV column is exactly a query
// parameter and there is nothing extra to learn or to document twice.
var optionPrefixes = []string{"style.", "encode.", "output."}

// payloadPrefix is the builder namespace. It is open-ended — a builder defines
// its own fields — so it lands in Item.Payload rather than Item.Options.
const payloadPrefix = "payload."

// utf8BOM is what Excel and Numbers put at the head of a CSV export. Left in
// place it would make the first column a name that reads as "id" but is not,
// failing the header check with a message no one can read because the
// offending bytes are invisible.
const utf8BOM = "\ufeff"

// ParseCSV turns a CSV document into items.
//
// The first row is a header. `id`, `data` and `type` map onto the item's own
// fields; every `style.*`, `encode.*` and `output.*` column becomes a
// per-item option, and every `payload.*` column becomes a builder field. A
// `data` column is required unless the header carries `type` together with at
// least one `payload.*` column, which is the shape of a batch built from
// structured data rather than from ready-made strings.
//
// Cell values stay strings. The request layer already parses text into every
// field's type — that is how the query string works — so converting here would
// only be a second, divergent parser.
//
// Errors name the row. A CSV of nine hundred lines that fails on one of them
// is useless without that number, and the row reported is the line in the
// document the caller uploaded, counting the header as row 1, so it matches
// what their editor shows.
func ParseCSV(text string) ([]Item, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("%w: the document is empty", ErrBadCSV)
	}

	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, utf8BOM)))
	// A ragged row is a mistake, not a shorthand: silently padding it would
	// produce codes encoding the wrong column. csv enforces this once the
	// header has set the width.
	r.FieldsPerRecord = 0
	// Leading space is part of a value in a barcode payload far more often
	// than it is decoration, so it is preserved.
	r.TrimLeadingSpace = false

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: row 1: %s", ErrBadCSV, csvCause(err))
	}

	cols, err := parseHeader(header)
	if err != nil {
		return nil, err
	}

	var items []Item
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: row %d: %s", ErrBadCSV, csvLine(err), csvCause(err))
		}

		// FieldPos reports the position in the original document, which is what
		// the caller's editor agrees with even when a quoted field spans lines.
		row, _ := r.FieldPos(0)

		item, empty := buildItem(cols, record)
		if empty {
			// A trailing row of commas is what a spreadsheet leaves behind on
			// export; rejecting it would fail almost every real upload.
			continue
		}
		if item.Data == "" && item.Type == "" {
			return nil, fmt.Errorf("%w: row %d: no data and no type; every row must set one",
				ErrBadCSV, row)
		}
		items = append(items, item)
	}

	return items, nil
}

// column describes what one header cell means.
type column struct {
	// name is the normalised header text.
	name string
	// kind selects where the cell's value goes.
	kind columnKind
}

// columnKind enumerates the destinations a column can have.
type columnKind int

const (
	kindIgnored columnKind = iota
	kindID
	kindData
	kindType
	kindOption
	kindPayload
)

// parseHeader validates the header row and records what each column sets.
func parseHeader(header []string) ([]column, error) {
	cols := make([]column, len(header))
	seen := make(map[string]bool, len(header))
	var hasData, hasType, hasPayload bool

	for i, raw := range header {
		name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, utf8BOM)))
		if name == "" {
			// An unnamed column cannot be addressed, and a spreadsheet export
			// often has one at the end; ignoring it is kinder than failing.
			cols[i] = column{kind: kindIgnored}
			continue
		}
		if seen[name] {
			return nil, fmt.Errorf("%w: row 1: column %q appears twice", ErrBadCSV, name)
		}
		seen[name] = true

		kind, err := classify(name)
		if err != nil {
			return nil, err
		}
		cols[i] = column{name: name, kind: kind}

		switch kind {
		case kindData:
			hasData = true
		case kindType:
			hasType = true
		case kindPayload:
			hasPayload = true
		case kindIgnored, kindID, kindOption:
			// These place no requirement on the header as a whole.
		}
	}

	if !hasData && (!hasType || !hasPayload) {
		return nil, fmt.Errorf("%w: row 1: no data column; a header without one must have "+
			"type and at least one payload.* column", ErrBadCSV)
	}
	return cols, nil
}

// classify maps a header name onto its destination.
func classify(name string) (columnKind, error) {
	switch name {
	case colID:
		return kindID, nil
	case colData:
		return kindData, nil
	case colType:
		return kindType, nil
	}
	if rest, ok := strings.CutPrefix(name, payloadPrefix); ok {
		if rest == "" {
			return kindIgnored, fmt.Errorf("%w: row 1: column %q names no payload field",
				ErrBadCSV, name)
		}
		return kindPayload, nil
	}
	for _, prefix := range optionPrefixes {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			if rest == "" {
				return kindIgnored, fmt.Errorf("%w: row 1: column %q names no option",
					ErrBadCSV, name)
			}
			return kindOption, nil
		}
	}
	return kindIgnored, fmt.Errorf("%w: row 1: unknown column %q: expected id, data, type, "+
		"or a style.*, encode.*, output.* or payload.* option", ErrBadCSV, name)
}

// buildItem assembles one item from a record. It reports whether every cell
// was blank, which marks a filler row rather than a broken one.
func buildItem(cols []column, record []string) (Item, bool) {
	var item Item
	empty := true

	for i, col := range cols {
		if i >= len(record) {
			break
		}
		value := record[i]
		if value != "" {
			empty = false
		}
		// A blank cell means "not set", not "set to empty": a CSV is a grid, so
		// every row carries every column whether it has an opinion or not.
		if value == "" || col.kind == kindIgnored {
			continue
		}

		switch col.kind {
		case kindID:
			item.ID = value
		case kindData:
			item.Data = value
		case kindType:
			item.Type = value
		case kindPayload:
			if item.Payload == nil {
				item.Payload = make(map[string]any)
			}
			item.Payload[strings.TrimPrefix(col.name, payloadPrefix)] = value
		case kindOption:
			if item.Options == nil {
				item.Options = make(map[string]any)
			}
			item.Options[col.name] = value
		case kindIgnored:
			// Unreachable: filtered above, but the compiler wants the arm.
		}
	}

	return item, empty
}

// csvLine extracts the document line a parse error refers to.
func csvLine(err error) int {
	var parseErr *csv.ParseError
	if errors.As(err, &parseErr) {
		return parseErr.Line
	}
	return 0
}

// csvCause reduces a csv error to its reason, dropping the "record on line N,
// parse error on line N, column M" prefix the package builds — the row is
// already in our own message and saying it three times helps nobody.
func csvCause(err error) string {
	var parseErr *csv.ParseError
	if errors.As(err, &parseErr) {
		if errors.Is(parseErr.Err, csv.ErrFieldCount) {
			return "wrong number of columns for this header"
		}
		return parseErr.Err.Error()
	}
	if errors.Is(err, io.EOF) {
		return "the document has no header row"
	}
	return "could not be parsed"
}
