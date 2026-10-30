package httpx

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers WebP with image.Decode
)

// Upstream profile pictures are often multi-megabyte originals while the
// largest avatar on the site is the ~200 CSS px profile picture, so one feed
// page could download several MB of avatars to draw 40px circles. Large still
// pictures are downscaled once when fetched, and the copy is what gets cached
// in memory, at the edge and in browsers.
const (
	// avatarThumbSize is the shorter side of a thumbnail: sharp for the
	// profile picture on 2x screens and for every smaller avatar.
	avatarThumbSize = 400
	// avatarThumbMaxPixels skips pictures whose header declares a bitmap too
	// large to decode safely on the small instance (a 12 MP decode and scale
	// peaks near 90 MB).
	avatarThumbMaxPixels   = 12 << 20
	avatarThumbJPEGQuality = 85
	// avatarThumbPNGMaxBytes keeps opaque PNG and WebP thumbnails lossless
	// (logos, pixel art) unless the PNG is photo-sized, then JPEG is used.
	avatarThumbPNGMaxBytes = 96 << 10
)

// avatarThumbSlot allows one decode per vCPU on the instance, which bounds the
// CPU and memory a burst of cold avatars can take.
var avatarThumbSlot = make(chan struct{}, 2)

// avatarThumbnail returns a downscaled copy of an avatar and its content type,
// or ok=false to serve the original unchanged: it is already small, animated,
// in a format Go cannot decode (GIF and AVIF pass through), too large to decode
// safely, or the copy would not be smaller.
func avatarThumbnail(ctx context.Context, body []byte, contentType string) ([]byte, string, bool) {
	isJPEG := contentType == "image/jpeg" || contentType == "image/jpg"
	switch {
	case isJPEG:
	case contentType == "image/png" && !pngIsAnimated(body):
	case contentType == "image/webp" && !webpIsAnimated(body):
	default:
		return nil, "", false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || cfg.Width*cfg.Height > avatarThumbMaxPixels {
		return nil, "", false
	}
	short := min(cfg.Width, cfg.Height)
	if short <= avatarThumbSize {
		return nil, "", false
	}

	select {
	case avatarThumbSlot <- struct{}{}:
		defer func() { <-avatarThumbSlot }()
	case <-ctx.Done():
		return nil, "", false
	}
	src, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, "", false
	}
	bounds := src.Bounds()
	thumb := image.NewRGBA(image.Rect(0, 0, bounds.Dx()*avatarThumbSize/short, bounds.Dy()*avatarThumbSize/short))
	xdraw.CatmullRom.Scale(thumb, thumb.Bounds(), src, bounds, xdraw.Src, nil)

	var out []byte
	thumbType := "image/png"
	switch {
	case !thumb.Opaque():
		out, err = encodeAvatarPNG(thumb)
	case isJPEG:
		// Browsers draw the original rotated by its EXIF orientation and in its
		// ICC color space; the re-encoded copy carries neither on its own.
		thumbType = "image/jpeg"
		out, err = encodeAvatarJPEG(orientImage(thumb, jpegOrientation(body)), jpegICCSegments(body))
	default:
		out, err = encodeAvatarPNG(thumb)
		if err == nil && len(out) > avatarThumbPNGMaxBytes {
			thumbType = "image/jpeg"
			out, err = encodeAvatarJPEG(thumb, nil)
		}
	}
	if err != nil || len(out) >= len(body) {
		return nil, "", false
	}
	return out, thumbType, true
}

func encodeAvatarPNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeAvatarJPEG encodes img and inserts icc (raw APP2 segments) right after
// the start-of-image marker.
func encodeAvatarJPEG(img image.Image, icc []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: avatarThumbJPEGQuality}); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(icc) == 0 {
		return out, nil
	}
	withICC := make([]byte, 0, len(out)+len(icc))
	withICC = append(withICC, out[:2]...)
	withICC = append(withICC, icc...)
	return append(withICC, out[2:]...), nil
}

// orientImage returns img transformed by an EXIF orientation (1-8).
func orientImage(img *image.RGBA, orientation int) *image.RGBA {
	if orientation < 2 || orientation > 8 {
		return img
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch orientation {
			case 2: // mirrored
				dx, dy = w-1-x, y
			case 3: // rotated 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirrored vertically
				dx, dy = x, h-1-y
			case 5: // transposed
				dx, dy = y, x
			case 6: // rotated 90 clockwise
				dx, dy = h-1-y, x
			case 7: // transversed
				dx, dy = h-1-y, w-1-x
			case 8: // rotated 90 counter-clockwise
				dx, dy = y, w-1-x
			}
			si, di := img.PixOffset(x, y), out.PixOffset(dx, dy)
			copy(out.Pix[di:di+4], img.Pix[si:si+4])
		}
	}
	return out
}

type jpegSegment struct {
	marker byte
	raw    []byte // marker, length and payload
	data   []byte // payload
}

// jpegSegments returns the header segments of a JPEG, up to the image data.
func jpegSegments(body []byte) []jpegSegment {
	if len(body) < 4 || body[0] != 0xFF || body[1] != 0xD8 {
		return nil
	}
	var segments []jpegSegment
	for i := 2; i+4 <= len(body); {
		if body[i] != 0xFF {
			break
		}
		marker := body[i+1]
		if marker == 0xFF { // fill byte
			i++
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // start of scan, end of image
			break
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) { // no length
			i += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(body[i+2:]))
		if n < 2 || i+2+n > len(body) {
			break
		}
		segments = append(segments, jpegSegment{marker: marker, raw: body[i : i+2+n], data: body[i+4 : i+2+n]})
		i += 2 + n
	}
	return segments
}

// jpegOrientation returns a JPEG's EXIF orientation, or 1 when it has none.
func jpegOrientation(body []byte) int {
	for _, segment := range jpegSegments(body) {
		if segment.marker != 0xE1 || !bytes.HasPrefix(segment.data, []byte("Exif\x00\x00")) {
			continue
		}
		tiff := segment.data[6:]
		if len(tiff) < 8 {
			return 1
		}
		var order binary.ByteOrder
		switch string(tiff[:2]) {
		case "II":
			order = binary.LittleEndian
		case "MM":
			order = binary.BigEndian
		default:
			return 1
		}
		ifd := int(order.Uint32(tiff[4:8]))
		if ifd < 8 || ifd+2 > len(tiff) {
			return 1
		}
		for i := range int(order.Uint16(tiff[ifd:])) {
			entry := ifd + 2 + i*12
			if entry+12 > len(tiff) {
				return 1
			}
			if order.Uint16(tiff[entry:]) == 0x0112 {
				if v := int(order.Uint16(tiff[entry+8:])); v >= 1 && v <= 8 {
					return v
				}
				return 1
			}
		}
		return 1
	}
	return 1
}

// jpegICCSegments returns a JPEG's ICC profile segments, ready to copy.
func jpegICCSegments(body []byte) []byte {
	var icc []byte
	for _, segment := range jpegSegments(body) {
		if segment.marker == 0xE2 && bytes.HasPrefix(segment.data, []byte("ICC_PROFILE\x00")) {
			icc = append(icc, segment.raw...)
		}
	}
	return icc
}

// pngIsAnimated reports whether a PNG is an APNG. Go decodes only the first
// frame, so a thumbnail would freeze the animation.
func pngIsAnimated(body []byte) bool {
	for i := 8; i+8 <= len(body); {
		switch string(body[i+4 : i+8]) {
		case "acTL":
			return true
		case "IDAT":
			return false
		}
		i += 12 + int(binary.BigEndian.Uint32(body[i:]))
	}
	return false
}

// webpIsAnimated reports whether an extended-format WebP has its animation flag.
func webpIsAnimated(body []byte) bool {
	return len(body) > 20 && string(body[:4]) == "RIFF" && string(body[8:16]) == "WEBPVP8X" && body[20]&0x02 != 0
}
