package httpx

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// noisyImage returns a deterministic picture that compresses about as badly
// as a photo, so encoded test originals have realistic sizes.
func noisyImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(1)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = byte(seed >> 24)
		if i%4 == 3 {
			img.Pix[i] = 0xFF
		}
	}
	return img
}

func encodeTestJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeTestPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withJPEGSegment inserts a raw segment right after the start-of-image marker.
func withJPEGSegment(body, segment []byte) []byte {
	out := append([]byte{}, body[:2]...)
	out = append(out, segment...)
	return append(out, body[2:]...)
}

func jpegAppSegment(marker byte, payload []byte) []byte {
	segment := []byte{0xFF, marker, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(len(payload)+2))
	return append(segment, payload...)
}

func exifOrientationSegment(orientation uint16) []byte {
	tiff := []byte{
		'M', 'M', 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08, // big-endian, IFD0 at offset 8
		0x00, 0x01, // one entry: Orientation, SHORT, count 1
		0x01, 0x12, 0x00, 0x03, 0x00, 0x00, 0x00, 0x01, byte(orientation >> 8), byte(orientation), 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, // no next IFD
	}
	return jpegAppSegment(0xE1, append([]byte("Exif\x00\x00"), tiff...))
}

func decodeThumb(t *testing.T, body []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("thumbnail does not decode: %v", err)
	}
	return img
}

func TestAvatarThumbnailDownscalesLargeJPEG(t *testing.T) {
	original := encodeTestJPEG(t, noisyImage(1200, 900))

	thumb, thumbType, ok := avatarThumbnail(context.Background(), original, "image/jpeg")

	if !ok || thumbType != "image/jpeg" {
		t.Fatalf("ok=%v type=%q, want a JPEG thumbnail", ok, thumbType)
	}
	if got := decodeThumb(t, thumb).Bounds().Size(); got != image.Pt(533, 400) {
		t.Fatalf("thumbnail size = %v, want shorter side %d with the aspect ratio kept", got, avatarThumbSize)
	}
	if len(thumb)*4 > len(original) {
		t.Fatalf("thumbnail is %d bytes for a %d byte original, want far smaller", len(thumb), len(original))
	}
}

func TestAvatarThumbnailKeepsSmallPictures(t *testing.T) {
	for _, size := range []image.Point{{300, 300}, {400, 2000}} {
		original := encodeTestJPEG(t, noisyImage(size.X, size.Y))
		if _, _, ok := avatarThumbnail(context.Background(), original, "image/jpeg"); ok {
			t.Fatalf("%v picture was resized, want it served as is", size)
		}
	}
}

func TestAvatarThumbnailKeepsTransparencyAsPNG(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 800, 600))
	noise := noisyImage(800, 600)
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			c := noise.RGBAAt(x, y)
			if x < 400 {
				c.A = 0
			}
			src.SetNRGBA(x, y, color.NRGBA{c.R, c.G, c.B, c.A})
		}
	}

	thumb, thumbType, ok := avatarThumbnail(context.Background(), encodeTestPNG(t, src), "image/png")

	if !ok || thumbType != "image/png" {
		t.Fatalf("ok=%v type=%q, want a PNG thumbnail", ok, thumbType)
	}
	img := decodeThumb(t, thumb)
	if _, _, _, a := img.At(50, 200).RGBA(); a != 0 {
		t.Fatalf("transparent area has alpha %d, want 0", a)
	}
	if _, _, _, a := img.At(480, 200).RGBA(); a != 0xFFFF {
		t.Fatalf("opaque area has alpha %d, want opaque", a)
	}
}

func TestAvatarThumbnailKeepsLosslessGraphicsAsPNG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 900, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 900; x++ {
			c := color.RGBA{0x20, 0x60, 0xC0, 0xFF}
			if (x/150+y/150)%2 == 0 {
				c = color.RGBA{0xF0, 0xF0, 0xF0, 0xFF}
			}
			src.SetRGBA(x, y, c)
		}
	}
	// Pad the original the way metadata-heavy exports are, so a smaller copy exists.
	original := append(encodeTestPNG(t, src), make([]byte, 64<<10)...)

	_, thumbType, ok := avatarThumbnail(context.Background(), original, "image/png")

	if !ok || thumbType != "image/png" {
		t.Fatalf("ok=%v type=%q, want a lossless PNG thumbnail for flat graphics", ok, thumbType)
	}
}

func TestAvatarThumbnailAppliesEXIFOrientation(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1600, 800))
	for y := 0; y < 800; y++ {
		for x := 0; x < 1600; x++ {
			c := color.RGBA{0xFF, 0, 0, 0xFF}
			if x >= 800 {
				c = color.RGBA{0, 0, 0xFF, 0xFF}
			}
			src.SetRGBA(x, y, c)
		}
	}
	original := withJPEGSegment(encodeTestJPEG(t, src), exifOrientationSegment(6))
	original = append(original, make([]byte, 256<<10)...) // keep the copy smaller than the original

	thumb, _, ok := avatarThumbnail(context.Background(), original, "image/jpeg")

	if !ok {
		t.Fatal("want a thumbnail")
	}
	img := decodeThumb(t, thumb)
	if got := img.Bounds().Size(); got != image.Pt(400, 800) {
		t.Fatalf("size = %v, want the 90° rotation applied (400x800)", got)
	}
	// Rotating clockwise moves the original left (red) half to the top.
	if r, _, b, _ := img.At(200, 150).RGBA(); r>>8 < 200 || b>>8 > 80 {
		t.Fatalf("top should be red, got r=%d b=%d", r>>8, b>>8)
	}
	if r, _, b, _ := img.At(200, 650).RGBA(); b>>8 < 200 || r>>8 > 80 {
		t.Fatalf("bottom should be blue, got r=%d b=%d", r>>8, b>>8)
	}
}

func TestAvatarThumbnailCopiesICCProfile(t *testing.T) {
	icc := jpegAppSegment(0xE2, append([]byte("ICC_PROFILE\x00\x01\x01"), "display-p3-profile-bytes"...))
	original := withJPEGSegment(encodeTestJPEG(t, noisyImage(1000, 1000)), icc)

	thumb, _, ok := avatarThumbnail(context.Background(), original, "image/jpeg")

	if !ok {
		t.Fatal("want a thumbnail")
	}
	if !bytes.Equal(thumb[2:2+len(icc)], icc) {
		t.Fatal("thumbnail should carry the original ICC profile right after SOI")
	}
	decodeThumb(t, thumb)
}

func TestAvatarThumbnailPassesThroughAnimationAndUnsupportedFormats(t *testing.T) {
	var gifBuf bytes.Buffer
	frame := image.NewPaletted(image.Rect(0, 0, 900, 900), color.Palette{color.Black, color.White})
	if err := gif.EncodeAll(&gifBuf, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	stillPNG := encodeTestPNG(t, noisyImage(900, 900))
	actl := []byte{0, 0, 0, 8, 'a', 'c', 'T', 'L', 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0}
	apng := append(append(append([]byte{}, stillPNG[:33]...), actl...), stillPNG[33:]...)
	animatedWebP := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x02"), make([]byte, 64)...)

	for name, tc := range map[string]struct {
		body        []byte
		contentType string
	}{
		"gif":           {gifBuf.Bytes(), "image/gif"},
		"apng":          {apng, "image/png"},
		"animated webp": {animatedWebP, "image/webp"},
		"avif":          {[]byte("\x00\x00\x00\x1cftypavif" + string(make([]byte, 64))), "image/avif"},
	} {
		if _, _, ok := avatarThumbnail(context.Background(), tc.body, tc.contentType); ok {
			t.Fatalf("%s was re-encoded, want it served as is", name)
		}
	}
}

func TestAvatarThumbnailSkipsOversizedDeclaredBitmap(t *testing.T) {
	// A tiny file whose header claims 20000x20000 must not be decoded.
	body := encodeTestPNG(t, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	binary.BigEndian.PutUint32(body[16:], 20000)
	binary.BigEndian.PutUint32(body[20:], 20000)
	binary.BigEndian.PutUint32(body[29:], crc32.ChecksumIEEE(body[12:29]))
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(body)); err != nil || cfg.Width != 20000 {
		t.Fatalf("test PNG header not accepted: %v %v", cfg, err)
	}

	if _, _, ok := avatarThumbnail(context.Background(), body, "image/png"); ok {
		t.Fatal("oversized bitmap was decoded")
	}
}

func TestHandleAvatarServesThumbnailOfLargePicture(t *testing.T) {
	original := encodeTestJPEG(t, noisyImage(1200, 1200))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(original)
	}))
	t.Cleanup(upstream.Close)
	srv, _ := testServer(t)
	pictureURL := upstream.URL + "/big.jpg"
	writeProfilePicture(t, srv, "alice", pictureURL)

	rr := httptest.NewRecorder()
	srv.handleAvatar(rr, httptest.NewRequest(http.MethodGet, "/avatar/alice?v="+avatarFingerprint(pictureURL), nil))

	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("status=%d type=%q, want a 200 JPEG", rr.Code, rr.Header().Get("Content-Type"))
	}
	if got := decodeThumb(t, rr.Body.Bytes()).Bounds().Size(); got != image.Pt(avatarThumbSize, avatarThumbSize) {
		t.Fatalf("served size = %v, want %dx%d", got, avatarThumbSize, avatarThumbSize)
	}
	if rr.Body.Len() >= len(original) || rr.Header().Get("Content-Length") != strconv.Itoa(rr.Body.Len()) {
		t.Fatalf("served %d bytes (Content-Length %q) for a %d byte original", rr.Body.Len(), rr.Header().Get("Content-Length"), len(original))
	}
}
