package render

import (
	"fmt"
	"image"
	"image/draw"
	"testing"

	xdraw "golang.org/x/image/draw"

	"framestudio-proto/src/model"
)

// Micro-benchmarks of the compositor stages on frame-sized buffers. They need no media
// and no environment. Run: GOMAXPROCS=4 nice -n 19 go test ./src/render -run x -bench Stage -benchmem

var stageSizes = [][2]int{{960, 540}, {1280, 720}, {1920, 1080}, {3840, 2160}}

func rgbaFrame(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	return img
}

func nrgbaFrame(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	return img
}

func eachSize(b *testing.B, f func(b *testing.B, w, h int)) {
	for _, s := range stageSizes {
		b.Run(fmt.Sprintf("%dx%d", s[0], s[1]), func(b *testing.B) {
			b.SetBytes(int64(s[0] * s[1] * 4))
			f(b, s[0], s[1])
		})
	}
}

// RGBA (what ffmpeg delivers) to NRGBA (what the compositor uses): allocates a frame.
func BenchmarkStageToNRGBA(b *testing.B) {
	eachSize(b, func(b *testing.B, w, h int) {
		src := rgbaFrame(w, h)
		b.ReportAllocs()
		for b.Loop() {
			toNRGBA(src)
		}
	})
}

// Same size, same type: the copy of drawScaled when the source already fits.
func BenchmarkStageDrawCopy(b *testing.B) {
	eachSize(b, func(b *testing.B, w, h int) {
		src, dst := nrgbaFrame(w, h), nrgbaFrame(w, h)
		for b.Loop() {
			drawScaled(dst, src, dst.Rect)
		}
	})
}

// Bilinear scale 1.5x down (e.g. a 1080p source into a 720p preview).
func BenchmarkStageDrawScaled(b *testing.B) {
	eachSize(b, func(b *testing.B, w, h int) {
		src, dst := nrgbaFrame(w*3/2, h*3/2), nrgbaFrame(w, h)
		for b.Loop() {
			drawScaled(dst, src, dst.Rect)
		}
	})
}

func BenchmarkStageOverLayer(b *testing.B) {
	eachSize(b, func(b *testing.B, w, h int) {
		l, dst := nrgbaFrame(w, h), nrgbaFrame(w, h)
		for b.Loop() {
			overLayer(dst, l, dst.Rect, 0.7)
		}
	})
}

func BenchmarkStageOverLayerOpaque(b *testing.B) {
	eachSize(b, func(b *testing.B, w, h int) {
		l, dst := nrgbaFrame(w, h), nrgbaFrame(w, h)
		for b.Loop() {
			overLayer(dst, l, dst.Rect, 1)
		}
	})
}

func BenchmarkStageGrade(b *testing.B) {
	g := model.Grade{Exposure: 0.2, Contrast: 0.15, Saturation: 0.2, Temperature: 0.1, Vignette: 0.3}
	eachSize(b, func(b *testing.B, w, h int) {
		img := nrgbaFrame(w, h)
		for b.Loop() {
			gradeRect(img, img.Rect, g)
		}
	})
}

func BenchmarkStageGradeSimple(b *testing.B) {
	g := model.Grade{Contrast: 0.15}
	eachSize(b, func(b *testing.B, w, h int) {
		img := nrgbaFrame(w, h)
		for b.Loop() {
			gradeRect(img, img.Rect, g)
		}
	})
}

func BenchmarkStageFill(b *testing.B) {
	eachSize(b, func(b *testing.B, w, h int) {
		img := nrgbaFrame(w, h)
		for b.Loop() {
			fillRect(img, img.Rect, project0Black)
		}
	})
}

// Allocating the output frame (image.NewNRGBA zeroes it) and the pooled layer.
func BenchmarkStageNewFrame(b *testing.B) {
	eachSize(b, func(b *testing.B, w, h int) {
		b.ReportAllocs()
		for b.Loop() {
			_ = image.NewNRGBA(image.Rect(0, 0, w, h))
		}
	})
}

func BenchmarkStageGetLayer(b *testing.B) {
	eachSize(b, func(b *testing.B, w, h int) {
		b.ReportAllocs()
		for b.Loop() {
			putLayer(getLayer(w, h))
		}
	})
}

func BenchmarkStageHistogram(b *testing.B) {
	eachSize(b, func(b *testing.B, w, h int) {
		img := nrgbaFrame(w, h)
		for b.Loop() {
			Histogram(img)
		}
	})
}

// What the fyne GL painter does on the UI thread with canvas.Image{Image: *image.NRGBA,
// ScaleMode: ImageScaleSmooth} whose pixel size differs from the widget: CatmullRom
// software scale (internal/painter.scaleImage) and, because the result is not an
// *image.RGBA, an NRGBA->RGBA draw.Draw before glTexImage2D (gl.imgToTexture). The GL
// upload itself is not included (no GPU here).
func BenchmarkFynePainterNRGBAtoTexture(b *testing.B) {
	// dw,dh is the widget's texture size; fyne scales whenever it differs from the image
	// size, but never up (pixW = min(dw, sw)), so a 1280x720 frame in a 1500x844 widget
	// is "scaled" 1280x720 -> 1280x720 (scale=true with equal sizes).
	for _, c := range []struct {
		sw, sh, dw, dh int
		same           bool // widget size equals image size: fyne skips the scale
	}{
		{960, 540, 1280, 720, false}, {1280, 720, 1280, 720, true}, {1280, 720, 1500, 844, false},
		{1280, 720, 1000, 563, false}, {1920, 1080, 1280, 720, false}, {1920, 1080, 1920, 1080, true},
		{1280, 720, 1281, 721, false},
	} {
		b.Run(fmt.Sprintf("%dx%d_in_%dx%d", c.sw, c.sh, c.dw, c.dh), func(b *testing.B) {
			src := nrgbaFrame(c.sw, c.sh)
			b.ReportAllocs()
			for b.Loop() {
				var im image.Image = src
				if !c.same {
					pw, ph := min(c.dw, c.sw), min(c.dh, c.sh)
					tex := image.NewNRGBA(image.Rect(0, 0, pw, ph))
					xdraw.CatmullRom.Scale(tex, tex.Rect, src, src.Rect, xdraw.Over, nil)
					im = tex
				}
				rgba := image.NewRGBA(image.Rect(0, 0, im.Bounds().Dx(), im.Bounds().Dy()))
				draw.Draw(rgba, rgba.Rect, im, im.Bounds().Min, draw.Over)
			}
		})
	}
}

// Same hand-off if the viewer gave fyne an *image.RGBA of the final size (what a fast path would do).
func BenchmarkFynePainterRGBADirect(b *testing.B) {
	src := rgbaFrame(1280, 720)
	for b.Loop() {
		_ = src.Pix // glTexImage2D(src.Pix): a memcpy-class DMA upload, no CPU work in Go
	}
}

var project0Black = model.RGBA{R: 0, G: 0, B: 0, A: 255}.NRGBA()
