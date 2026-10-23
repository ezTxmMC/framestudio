package export

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
)

// burstProject has a colour clip of n frames and a 1 kHz burst (1.0 s to 1.2 s of the
// file) whose audio clip starts at the odd frame 13.
func burstProject(t *testing.T, r model.Rate, n int) (*model.Project, float64) {
	t.Helper()
	wav := filepath.Join(t.TempDir(), "burst.wav")
	out, err := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
		"-i", "aevalsrc=if(between(t\\,1.0\\,1.2)\\,0.8*sin(2*PI*1000*t)\\,0):s=48000:d=5", wav).CombinedOutput()
	if err != nil {
		t.Fatalf("generate burst: %v %s", err, out)
	}
	p := model.NewProject("Burst")
	p.Settings.Width, p.Settings.Height = 160, 90
	p.Settings.FPS = r
	must(t, p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Farbe", 0, n)))
	m := model.MediaItem{ID: model.NewID(), Name: "burst", Path: wav, Kind: model.KindAudio, Duration: 5 * time.Second}
	p.AddMedia(m)
	const start = 13
	c := p.ClipFromMedia(m, start, n-start)
	c.Length = n - start
	must(t, p.PlaceClip(p.Tracks[2].ID, c))
	return p, r.FramesToSeconds(start) + 1.0
}

// onset decodes the audio of path to mono float samples at 48 kHz and returns the time
// of the first sample above half the burst level.
func onset(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-i", path,
		"-map", "0:a:0", "-ac", "1", "-ar", "48000", "-f", "f32le", "-").Output()
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	for i := 0; i+4 <= len(out); i += 4 {
		v := math.Float32frombits(binary.LittleEndian.Uint32(out[i:]))
		if math.Abs(float64(v)) > 0.4 {
			return float64(i/4) / 48000
		}
	}
	t.Fatal("no burst in the exported audio")
	return 0
}

// The burst lands at the right sample in WAV (sample exact) and MP4 (AAC, within 1.5 ms)
// exports at every rate, also when the export rate differs from the project rate.
func TestExportBurstSync(t *testing.T) {
	needFFmpeg(t)
	r := render.New(media.NewCache())
	for _, rate := range []model.Rate{model.NTSC24, model.Rate25, model.NTSC30, model.NTSC60} {
		p, want := burstProject(t, rate, rate.SecondsToFrames(4))
		dir := t.TempDir()
		wavPath := filepath.Join(dir, "a.wav")
		if err := Run(context.Background(), p, r, Options{Format: FormatWAV, Path: wavPath, IncludeAudio: true}, nil); err != nil {
			t.Fatal(err)
		}
		if got := onset(t, wavPath); math.Abs(got-want) > 0.001 {
			t.Errorf("%v WAV: onset %.5f s, want %.5f s", rate, got, want)
		}
		for _, override := range []model.Rate{{}, model.NTSC30, model.Rate25} {
			mp4 := filepath.Join(dir, "v.mp4")
			o := Options{Format: FormatMP4, Path: mp4, Width: 160, Height: 90, Quality: QualityLow, IncludeAudio: true, FPS: override}
			if err := Run(context.Background(), p, r, o, nil); err != nil {
				t.Fatal(err)
			}
			if got := onset(t, mp4); math.Abs(got-want) > 0.0015 {
				t.Errorf("%v MP4 (fps override %q): onset %.5f s, want %.5f s", rate, override.String(), got, want)
			}
			v, a := probeRate(t, mp4)
			out := OutputRate(o, rate)
			if math.Abs(v.dur-a.dur) > 1/out.Float()+0.001 || math.Abs(v.dur-rate.FramesToSeconds(p.Tracks[0].Clips[0].Length)) > 1/out.Float()+0.001 {
				t.Errorf("%v MP4 (fps override %q): video %.4f s audio %.4f s", rate, override.String(), v.dur, a.dur)
			}
		}
	}
}

func TestExportPNGSequenceCount(t *testing.T) {
	needFFmpeg(t)
	r := render.New(media.NewCache())
	for _, rate := range []model.Rate{model.NTSC24, model.NTSC30, model.NTSC60} {
		p := rateProject(t, rate, 37)
		dir := filepath.Join(t.TempDir(), "png")
		if err := Run(context.Background(), p, r, Options{Format: FormatPNG, Path: dir, Width: 64, Height: 36}, nil); err != nil {
			t.Fatal(err)
		}
		ents, err := os.ReadDir(dir)
		must(t, err)
		if len(ents) != 37 {
			t.Errorf("%v: %d PNG files, want 37", rate, len(ents))
		}
	}
}
