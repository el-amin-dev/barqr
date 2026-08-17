package encoder

// zintReason is the explanation attached to every symbology this build knows
// of but cannot produce. They are all covered by the optional zint linkage; the
// default build lists them so that a caller learns the symbology exists and
// what it would take to get it, instead of an "unknown symbology" that reads
// like a typo.
const zintReason = "requires the full build (zint)"

func init() {
	for _, caps := range zintOnly() {
		RegisterUnavailable(caps)
	}
}

// zintOnly returns the symbologies the default build declares but cannot
// encode. Capabilities are filled in from each specification so that
// /v1/symbologies stays useful before the full build is installed: a caller can
// see that Code 11 is numeric and that MaxiCode is fixed-size without having to
// try an encode first.
func zintOnly() []Capabilities {
	return []Capabilities{
		{
			Name:      "code11",
			Title:     "Code 11",
			Kind:      Kind1D,
			Charset:   "digits 0-9 and the dash",
			QuietZone: 10,
			HRI:       true,
			Reason:    zintReason,
		},
		{
			Name:      "msi",
			Title:     "MSI Plessey",
			Kind:      Kind1D,
			Charset:   "digits 0-9",
			QuietZone: 10,
			HRI:       true,
			Reason:    zintReason,
		},
		{
			Name:      "plessey",
			Title:     "Plessey",
			Kind:      Kind1D,
			Charset:   "hexadecimal digits 0-9 and A-F",
			QuietZone: 10,
			HRI:       true,
			Reason:    zintReason,
		},
		{
			Name:      "telepen",
			Title:     "Telepen",
			Kind:      Kind1D,
			Charset:   "the full 7-bit ASCII set",
			QuietZone: 10,
			HRI:       true,
			Reason:    zintReason,
		},
		{
			Name:      "pharmacode",
			Title:     "Pharmacode",
			Kind:      Kind1D,
			Charset:   "a single integer from 3 to 131070",
			QuietZone: 10,
			HRI:       false,
			Reason:    zintReason,
			Notes:     "the Laetus pharmaceutical code; the number itself is never printed alongside it",
		},
		{
			Name:      "rm4scc",
			Title:     "Royal Mail 4-State (RM4SCC)",
			Kind:      Kind1D,
			Charset:   "digits 0-9 and uppercase A-Z",
			QuietZone: 2,
			HRI:       false,
			Reason:    zintReason,
			Notes:     "a height-modulated postal code: bars carry data in their ascenders and descenders",
		},
		{
			Name:      "maxicode",
			Title:     "MaxiCode",
			Kind:      Kind2D,
			Charset:   "any ASCII text; a structured carrier message uses its own field format",
			MaxLength: 93,
			QuietZone: 1,
			HRI:       false,
			Reason:    zintReason,
			Notes:     "fixed size: a hexagonal grid of one inch square around a bullseye finder",
		},
		{
			Name:      "dotcode",
			Title:     "DotCode",
			Kind:      Kind2D,
			Charset:   "any byte data; GS1 application identifiers are supported",
			QuietZone: 3,
			HRI:       false,
			Reason:    zintReason,
			Notes:     "built from separated dots so that it survives high-speed inkjet printing",
		},
		{
			Name:      "gs1-128",
			Title:     "GS1-128",
			Kind:      Kind1D,
			Charset:   "GS1 application identifiers with ASCII data",
			QuietZone: 10,
			HRI:       true,
			Reason:    zintReason,
			Notes:     "Code 128 with a leading FNC1 and validated application identifiers",
		},
		{
			Name:      "gs1-datamatrix",
			Title:     "GS1 DataMatrix",
			Kind:      Kind2D,
			Charset:   "GS1 application identifiers with ASCII data",
			QuietZone: 1,
			HRI:       false,
			Reason:    zintReason,
			Notes:     "Data Matrix ECC 200 with a leading FNC1 and validated application identifiers",
		},
		{
			Name:         "databar",
			Title:        "GS1 DataBar Omnidirectional",
			Kind:         Kind1D,
			Charset:      "digits 0-9",
			FixedLengths: []int{13, 14},
			QuietZone:    1,
			HRI:          true,
			Reason:       zintReason,
			Notes:        "carries a GTIN-14 in a symbol short enough for loose produce",
		},
		{
			Name:      "databar-expanded",
			Title:     "GS1 DataBar Expanded",
			Kind:      Kind1D,
			Charset:   "GS1 application identifiers with alphanumeric data",
			MaxLength: 74,
			QuietZone: 1,
			HRI:       true,
			Reason:    zintReason,
			Notes:     "up to 74 digits or 41 alphanumeric characters, optionally stacked",
		},
		{
			Name:         "postnet",
			Title:        "POSTNET",
			Kind:         Kind1D,
			Charset:      "digits 0-9",
			FixedLengths: []int{5, 9, 11},
			QuietZone:    2,
			HRI:          false,
			Reason:       zintReason,
			Notes:        "the retired US postal code: ZIP, ZIP+4, or ZIP+4 with a delivery point",
		},
		{
			Name:         "planet",
			Title:        "PLANET",
			Kind:         Kind1D,
			Charset:      "digits 0-9",
			FixedLengths: []int{11, 13},
			QuietZone:    2,
			HRI:          false,
			Reason:       zintReason,
			Notes:        "the US postal tracking counterpart to POSTNET, with its bar heights inverted",
		},
		{
			Name:      "japanpost",
			Title:     "Japan Post 4-State",
			Kind:      Kind1D,
			Charset:   "digits 0-9, uppercase A-Z and the dash",
			QuietZone: 2,
			HRI:       false,
			Reason:    zintReason,
		},
		{
			Name:      "kix",
			Title:     "Dutch Post KIX",
			Kind:      Kind1D,
			Charset:   "digits 0-9 and uppercase A-Z",
			QuietZone: 2,
			HRI:       false,
			Reason:    zintReason,
			Notes:     "the same four bar states as RM4SCC, without the start, stop and check bars",
		},
		{
			Name:      "code16k",
			Title:     "Code 16K",
			Kind:      Kind2D,
			Charset:   "the full ASCII set",
			QuietZone: 10,
			HRI:       false,
			Reason:    zintReason,
			Notes:     "Code 128 character sets stacked over up to 16 rows",
		},
		{
			Name:      "codablock-f",
			Title:     "Codablock-F",
			Kind:      Kind2D,
			Charset:   "the full ASCII set",
			QuietZone: 10,
			HRI:       false,
			Reason:    zintReason,
			Notes:     "Code 128 rows stacked with row indicators, readable by a linear scanner",
		},
		{
			Name:      "hanxin",
			Title:     "Han Xin Code",
			Kind:      Kind2D,
			ECCLevels: []string{"L", "M", "Q", "H"},
			Charset:   "any UTF-8 text; GB 18030 Chinese characters are packed densely",
			QuietZone: 3,
			HRI:       false,
			Reason:    zintReason,
		},
		{
			Name:      "grid-matrix",
			Title:     "Grid Matrix",
			Kind:      Kind2D,
			ECCLevels: []string{"L", "M", "Q", "H"},
			Charset:   "any UTF-8 text; GB 2312 Chinese characters are packed densely",
			QuietZone: 3,
			HRI:       false,
			Reason:    zintReason,
		},
	}
}
