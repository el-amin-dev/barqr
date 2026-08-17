package writer

import (
	"encoding/base64"
	"strings"

	"github.com/el-amin-dev/barqr/internal/render"
)

// DataURI is the registry name of the data-URI writer.
const DataURI = "datauri"

func init() { Register(dataURIWriter{}) }

// dataURIWriter wraps another format as an RFC 2397 data URI.
//
// The output is a single line that can be pasted straight into an img src, a
// CSS background, or an email template, which is why it is text rather than
// binary even though its payload is not.
//
// It always wraps PNG. OutputOpts carries no field naming the inner format, so
// rather than invent one here — where it could not be validated, documented in
// the API, or discovered by a caller — the choice is fixed to the one format
// that is lossless, alpha-capable and understood by every browser. Sizing and
// colour options reach the inner writer unchanged.
type dataURIWriter struct{}

func (dataURIWriter) Name() string      { return DataURI }
func (dataURIWriter) MIME() string      { return "text/plain; charset=utf-8" }
func (dataURIWriter) Extension() string { return "txt" }
func (dataURIWriter) Binary() bool      { return false }

func (dataURIWriter) Write(c render.Canvas, o OutputOpts) ([]byte, error) {
	inner := pngWriter{}

	// The inner writer must not see Format: it would be the outer format name
	// and is not something a writer should read about itself.
	in := o
	in.Format = inner.Name()

	body, err := inner.Write(c, in)
	if err != nil {
		return nil, err
	}

	// PNG is binary, so the payload is base64 rather than percent-encoded.
	// Standard encoding with padding is what browsers accept.
	var b strings.Builder
	b.Grow(len("data:;base64,") + len(inner.MIME()) + base64.StdEncoding.EncodedLen(len(body)))
	b.WriteString("data:")
	b.WriteString(inner.MIME())
	b.WriteString(";base64,")
	b.WriteString(base64.StdEncoding.EncodeToString(body))
	return []byte(b.String()), nil
}
