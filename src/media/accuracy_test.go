package media

import (
	"image"
	"path/filepath"
	"strconv"
	"testing"

	"framestudio-proto/src/model"
)

// barcodeVideo writes a lossless clip whose frames carry their own index as a 16 bit
// barcode (8 px per bit), so a decoded frame tells which source frame it is.
func barcodeVideo(t *testing.T, rate string, secs int) model.MediaItem {
	t.Helper()
	needFFmpeg(t)
	path := filepath.Join(t.TempDir(), "bar.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "color=c=black:size=128x16:rate="+rate+":duration="+strconv.Itoa(secs)+
		",format=gray,geq=lum='if(bitand(floor(N/pow(2,floor(X/8))),1),235,16)',format=yuv444p",
		"-c:v", "libx264", "-qp", "0", "-g", "25", "-pix_fmt", "yuv444p", path)
	return mustProbe(t, path)
}

func readBarcode(img image.Image) int {
	r := img.(*image.RGBA)
	b := r.Bounds()
	v := 0
	for bit := 0; bit < 16; bit++ {
		x, y := b.Min.X+bit*b.Dx()/16+b.Dx()/32, b.Min.Y+b.Dy()/2
		if r.RGBAAt(x, y).R > 125 {
			v |= 1 << bit
		}
	}
	return v
}

// Frame k of a project at rate P shows the source frame at time k/P (floor or nearest
// of k*S/P), for equal and different source and project rates, by every access path.
func TestBarcodeFrameAccuracy(t *testing.T) {
	needFFmpeg(t)
	rates := map[string]model.Rate{"24000/1001": model.NTSC24, "25": model.Rate25, "30000/1001": model.NTSC30, "60000/1001": model.NTSC60}
	for srcS, src := range rates {
		m := barcodeVideo(t, srcS, 6)
		if m.FPS != src {
			t.Fatalf("probe %s: %v", srcS, m.FPS)
		}
		for projS, proj := range rates {
			c := NewCache()
			r, err := OpenVideo(m, 0, proj, 128, 16)
			if err != nil {
				t.Fatal(err)
			}
			seq := readAll(t, r)
			for _, k := range []int{0, 1, 2, 17, 59, 60, 101, 150} {
				if k >= len(seq) {
					continue
				}
				// the source frame shown must be on screen during the project frame's
				// slot [t-0.5/P, t+0.5/P] (what ffmpeg's fps filter picks)
				tt, half := float64(k)/proj.Float(), 0.5/proj.Float()
				check := func(how string, got int) {
					lo, hi := float64(got)/src.Float(), float64(got+1)/src.Float()
					if lo > tt+half+1e-9 || hi < tt-half-1e-9 {
						t.Errorf("src %s proj %s %s: frame %d shows source %d (%.4f-%.4f s), want time %.4f", srcS, projS, how, k, got, lo, hi, tt)
					}
				}
				check("sequential", readBarcode(seq[k]))
				img, err := c.VideoFrame(m, k, proj, 128, 16)
				if err != nil {
					t.Fatal(err)
				}
				check("VideoFrame", readBarcode(img))
				img, err = c.PlaybackFrame(m, k, proj, 128, 16, 1)
				if err != nil {
					t.Fatal(err)
				}
				check("PlaybackFrame", readBarcode(img))
			}
			c.Close()
		}
	}
}
