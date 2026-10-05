package server

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
)

// imagePreviewMaxSide bounds the longest side of the preview a calling model
// receives. Vision models downscale to roughly this size anyway, so sending
// the full-resolution file only spends the caller's context window.
const imagePreviewMaxSide = 768

// imagePreview returns a downscaled copy for inline display. Opaque images
// become JPEG; images with transparency stay PNG so a cutout still looks like
// one. Anything that cannot be decoded (WebP, for instance) is returned
// unchanged.
func imagePreview(data []byte) ([]byte, string) {
	original := http.DetectContentType(data)
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, original
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return data, original
	}
	scale := 1.0
	if longest := max(w, h); longest > imagePreviewMaxSide {
		scale = float64(imagePreviewMaxSide) / float64(longest)
	}
	dw, dh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	if scale == 1 {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	} else {
		downscaleBox(dst, src)
	}

	var out bytes.Buffer
	mimeType := "image/jpeg"
	if hasTransparency(dst) {
		mimeType = "image/png"
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, dst)
	} else {
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 82})
	}
	if err != nil || out.Len() >= len(data) {
		return data, original
	}
	return out.Bytes(), mimeType
}

// downscaleBox averages every source pixel that falls into each destination
// pixel, which keeps text and thin lines legible where nearest-neighbour
// sampling would drop them.
func downscaleBox(dst *image.NRGBA, src image.Image) {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := dst.Bounds().Dx(), dst.Bounds().Dy()
	for y := range dh {
		y0, y1 := b.Min.Y+y*sh/dh, b.Min.Y+max((y+1)*sh/dh, y*sh/dh+1)
		for x := range dw {
			x0, x1 := b.Min.X+x*sw/dw, b.Min.X+max((x+1)*sw/dw, x*sw/dw+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := color.NRGBAModel.Convert(src.At(sx, sy)).(color.NRGBA)
					// Weight colour by alpha so transparent pixels do not darken edges.
					r += uint64(c.R) * uint64(c.A)
					g += uint64(c.G) * uint64(c.A)
					bl += uint64(c.B) * uint64(c.A)
					a += uint64(c.A)
					n++
				}
			}
			if a == 0 {
				dst.SetNRGBA(x, y, color.NRGBA{})
				continue
			}
			dst.SetNRGBA(x, y, color.NRGBA{R: uint8(r / a), G: uint8(g / a), B: uint8(bl / a), A: uint8(a / n)})
		}
	}
}

func hasTransparency(img *image.NRGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xff {
			return true
		}
	}
	return false
}
