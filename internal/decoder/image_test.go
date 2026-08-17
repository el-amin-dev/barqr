package decoder

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// solidPNG encodes a plain white image of the given size as PNG. It is the
// smallest thing that is unambiguously a real, decodable image.
func solidPNG(t *testing.T, w, h int) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// bombPNG builds a tiny PNG whose IHDR chunk lies about its dimensions.
//
// This is the decompression bomb in its simplest form: a couple of hundred
// bytes on the wire that ask a decoder to allocate gigabytes. The header is
// rewritten in place and its CRC recomputed so the file is well formed right
// up to the point where the guard must reject it.
func bombPNG(t *testing.T, w, h uint32) []byte {
	t.Helper()

	raw := solidPNG(t, 1, 1)

	// Layout: 8-byte signature, then a chunk of 4-byte length, 4-byte type,
	// payload, 4-byte CRC. IHDR is always the first chunk, and its payload is
	// width, height, then five bytes of format flags.
	const sig, lenSize, typeSize = 8, 4, 4
	typeAt := sig + lenSize
	dataAt := typeAt + typeSize

	if got := string(raw[typeAt:dataAt]); got != "IHDR" {
		t.Fatalf("first chunk is %q, want IHDR", got)
	}

	out := bytes.Clone(raw)
	binary.BigEndian.PutUint32(out[dataAt:], w)
	binary.BigEndian.PutUint32(out[dataAt+4:], h)

	const ihdrLen = 13
	crc := crc32.ChecksumIEEE(out[typeAt : dataAt+ihdrLen])
	binary.BigEndian.PutUint32(out[dataAt+ihdrLen:], crc)
	return out
}

func TestLoadImageAcceptsEveryRegisteredContainer(t *testing.T) {
	t.Parallel()

	src := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			src.SetNRGBA(x, y, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF})
		}
	}

	encoders := map[string]func() []byte{
		"png": func() []byte { return solidPNG(t, 16, 16) },
		"jpeg": func() []byte {
			var b bytes.Buffer
			if err := jpeg.Encode(&b, src, nil); err != nil {
				t.Fatalf("jpeg.Encode: %v", err)
			}
			return b.Bytes()
		},
		"gif": func() []byte {
			var b bytes.Buffer
			if err := gif.Encode(&b, src, nil); err != nil {
				t.Fatalf("gif.Encode: %v", err)
			}
			return b.Bytes()
		},
	}

	for name, enc := range encoders {
		t.Run(name, func(t *testing.T) {
			img, err := LoadImage(t.Context(), enc(), Options{})
			if err != nil {
				t.Fatalf("LoadImage(%s) = %v", name, err)
			}
			if got := img.Bounds().Dx(); got != 16 {
				t.Fatalf("LoadImage(%s) width = %d, want 16", name, got)
			}
		})
	}
}

func TestLoadImageRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		opts Options
		want error
	}{
		{
			name: "empty input",
			data: nil,
			want: ErrUnsupportedImage,
		},
		{
			name: "random bytes",
			data: []byte("this is definitely not an image, it is a sentence"),
			want: ErrUnsupportedImage,
		},
		{
			name: "a plausible header with no image behind it",
			data: []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 13},
			want: ErrUnsupportedImage,
		},
		{
			name: "input larger than MaxBytes",
			data: bytes.Repeat([]byte{0}, 2048),
			opts: Options{MaxBytes: 1024},
			want: ErrImageTooLarge,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := LoadImage(t.Context(), tc.data, tc.opts); !errors.Is(err, tc.want) {
				t.Fatalf("LoadImage = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestLoadImageRejectsADeclaredBombBeforeDecoding is the security test the
// whole file exists for: the rejection must come from the header, while the
// input is still a couple of hundred bytes.
func TestLoadImageRejectsADeclaredBombBeforeDecoding(t *testing.T) {
	t.Parallel()

	bomb := bombPNG(t, 50_000, 50_000)
	if len(bomb) > 512 {
		t.Fatalf("crafted bomb is %d bytes; the point is that it is tiny", len(bomb))
	}

	// The header must parse, or the test would be proving nothing about the
	// pixel guard.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(bomb))
	if err != nil {
		t.Fatalf("crafted bomb has an unreadable header: %v", err)
	}
	if cfg.Width != 50_000 || cfg.Height != 50_000 {
		t.Fatalf("crafted bomb declares %dx%d, want 50000x50000", cfg.Width, cfg.Height)
	}

	if _, err := LoadImage(t.Context(), bomb, Options{}); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("LoadImage(bomb) = %v, want ErrImageTooLarge", err)
	}
}

func TestLoadImageEnforcesMaxPixels(t *testing.T) {
	t.Parallel()

	data := solidPNG(t, 32, 32)

	if _, err := LoadImage(t.Context(), data, Options{MaxPixels: 100}); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("LoadImage with MaxPixels=100 = %v, want ErrImageTooLarge", err)
	}
	if _, err := LoadImage(t.Context(), data, Options{MaxPixels: 32 * 32}); err != nil {
		t.Fatalf("LoadImage with an exactly sufficient MaxPixels = %v", err)
	}
}

func TestLoadImageRejectsTruncatedPixelData(t *testing.T) {
	t.Parallel()

	full := solidPNG(t, 64, 64)
	truncated := full[:len(full)/2]

	_, err := LoadImage(t.Context(), truncated, Options{})
	if !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("LoadImage(truncated png) = %v, want ErrUnsupportedImage", err)
	}
}

func TestLoadImageHonoursACancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := LoadImage(ctx, solidPNG(t, 32, 32), Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadImage with a cancelled context = %v, want context.Canceled", err)
	}
}

// TestSafelyConvertsAPanic covers the backstop around third-party decoders.
// No shipped decoder is expected to panic; the point is that if one does, the
// request fails rather than the process.
func TestSafelyConvertsAPanic(t *testing.T) {
	t.Parallel()

	err := safely(func() error { panic("a slice bounds bug in some parser") })
	if !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("safely(panic) = %v, want ErrUnsupportedImage", err)
	}
	if strings.Contains(err.Error(), "slice bounds") {
		t.Fatalf("safely leaked the panic value into %q", err)
	}

	sentinel := errors.New("ordinary failure")
	if got := safely(func() error { return sentinel }); !errors.Is(got, sentinel) {
		t.Fatalf("safely passed through %v, want %v", got, sentinel)
	}
}

func TestDataFromURI(t *testing.T) {
	t.Parallel()

	payload := []byte{0x89, 'P', 'N', 'G'}
	b64 := base64.StdEncoding.EncodeToString(payload)

	tests := []struct {
		name      string
		in        string
		want      []byte
		wantMedia string
		wantErr   bool
	}{
		{
			name:      "base64 png",
			in:        "data:image/png;base64," + b64,
			want:      payload,
			wantMedia: "image/png",
		},
		{
			name:      "unpadded and wrapped base64",
			in:        "data:image/png;base64," + strings.TrimRight(b64, "=") + "\n",
			want:      payload,
			wantMedia: "image/png",
		},
		{
			name:      "uppercase scheme and media type",
			in:        "DATA:IMAGE/PNG;BASE64," + b64,
			want:      payload,
			wantMedia: "image/png",
		},
		{
			name:      "percent-encoded payload",
			in:        "data:image/gif,GIF89%61",
			want:      []byte("GIF89a"),
			wantMedia: "image/gif",
		},
		{
			name:      "charset parameter before base64",
			in:        "data:image/webp;charset=binary;base64," + b64,
			want:      payload,
			wantMedia: "image/webp",
		},
		{name: "not a data uri", in: "https://example.com/a.png", wantErr: true},
		{name: "no comma", in: "data:image/png;base64" + b64, wantErr: true},
		{name: "non-image media type", in: "data:text/html;base64," + b64, wantErr: true},
		{name: "absent media type defaults to text", in: "data:;base64," + b64, wantErr: true},
		{name: "invalid base64", in: "data:image/png;base64,!!!not base64!!!", wantErr: true},
		{name: "invalid percent-encoding", in: "data:image/png,%zz", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, media, err := DataFromURI(tc.in)
			if tc.wantErr {
				if !errors.Is(err, ErrUnsupportedImage) {
					t.Fatalf("DataFromURI(%q) error = %v, want ErrUnsupportedImage", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("DataFromURI(%q) = %v", tc.in, err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("DataFromURI(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if media != tc.wantMedia {
				t.Fatalf("DataFromURI(%q) media = %q, want %q", tc.in, media, tc.wantMedia)
			}
		})
	}
}

func TestOptionsNormaliseAppliesTheDefaultLimits(t *testing.T) {
	t.Parallel()

	got := Options{}.normalise()
	if got.MaxPixels != DefaultMaxPixels || got.MaxBytes != DefaultMaxBytes {
		t.Fatalf("Options{}.normalise() = %+v, want the package defaults", got)
	}

	// A negative value is a caller mistake, not a request for "unlimited":
	// there is no way to switch the guards off, by design.
	got = Options{MaxPixels: -1, MaxBytes: -1}.normalise()
	if got.MaxPixels != DefaultMaxPixels || got.MaxBytes != DefaultMaxBytes {
		t.Fatalf("negative limits normalised to %+v, want the package defaults", got)
	}

	got = Options{MaxPixels: 7, MaxBytes: 9}.normalise()
	if got.MaxPixels != 7 || got.MaxBytes != 9 {
		t.Fatalf("normalise overwrote explicit limits: %+v", got)
	}

	if got := DefaultOptions(); got.MaxPixels != DefaultMaxPixels {
		t.Fatalf("DefaultOptions() = %+v", got)
	}
}
