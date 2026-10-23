package render

import (
	"os/exec"
	"path/filepath"
	"testing"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
)

// A clip with an offset into a 25 fps file inside a 29.97 and a 23.976 project shows, at
// timeline frame f, the source frame at time (SourceIn + f - Start) / project rate.
func TestRenderShowsSourceTime(t *testing.T) {
	if !media.FFmpegAvailable() {
		t.Skip("ffmpeg nicht installiert")
	}
	path := filepath.Join(t.TempDir(), "bar.mp4")
	out, err := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-y", "-f", "lavfi", "-i",
		"color=c=black:size=128x16:rate=25:duration=6,format=gray,geq=lum='if(bitand(floor(N/pow(2,floor(X/8))),1),235,16)',format=yuv444p",
		"-c:v", "libx264", "-qp", "0", "-g", "25", "-pix_fmt", "yuv444p", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	m, err := media.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	cache := media.NewCache()
	defer cache.Close()
	rd := New(cache)
	for _, rate := range []model.Rate{model.NTSC30, model.NTSC24, model.NTSC60} {
		p := model.NewProject("b")
		p.Settings.Width, p.Settings.Height, p.Settings.FPS = 128, 16, rate
		p.AddMedia(m)
		c := p.ClipFromMedia(m, 3, 100)
		c.SourceIn, c.Length = 5, 100
		if err := p.PlaceClip(p.Tracks[0].ID, c); err != nil {
			t.Fatal(err)
		}
		for _, f := range []int{3, 10, 40, 99} {
			img := rd.Frame(p, f, 128, 16)
			got := 0
			for bit := 0; bit < 16; bit++ {
				if img.NRGBAAt(bit*8+4, 8).R > 125 {
					got |= 1 << bit
				}
			}
			tt := rate.FramesToSeconds(c.SourceIn + f - c.Start)
			lo, hi := float64(got)/25, float64(got+1)/25
			half := 0.5 / rate.Float()
			if lo > tt+half+1e-9 || hi < tt-half-1e-9 {
				t.Errorf("%v frame %d shows source frame %d (%.3f-%.3f s), want time %.3f s", rate, f, got, lo, hi, tt)
			}
		}
	}
}
