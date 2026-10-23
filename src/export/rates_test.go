package export

import (
	"context"
	"encoding/json"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
)

// rateProject is n frames at the given rate: a colour clip and a tone of the same length.
func rateProject(t *testing.T, r model.Rate, n int) *model.Project {
	t.Helper()
	p := testProject(t, 5)
	p.Settings.FPS = r
	for ti := range p.Tracks {
		p.Tracks[ti].Clips = nil
	}
	must(t, p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Farbe", 0, n)))
	m := p.Media[0]
	c := p.ClipFromMedia(m, 0, 0)
	c.Length = n
	must(t, p.PlaceClip(p.Tracks[2].ID, c))
	return p
}

type rateProbe struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		RFrameRate   string `json:"r_frame_rate"`
		NbReadFrames string `json:"nb_read_frames"`
		Duration     string `json:"duration"`
	} `json:"streams"`
}

func probeRate(t *testing.T, path string) (video, audio struct {
	rate   string
	frames int
	dur    float64
	w, h   int
}) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-show_streams", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var pr rateProbe
	must(t, json.Unmarshal(out, &pr))
	for _, s := range pr.Streams {
		d, _ := strconv.ParseFloat(s.Duration, 64)
		n, _ := strconv.Atoi(s.NbReadFrames)
		switch s.CodecType {
		case "video":
			video.rate, video.frames, video.dur, video.w, video.h = s.RFrameRate, n, d, s.Width, s.Height
		case "audio":
			audio.rate, audio.frames, audio.dur = s.RFrameRate, n, d
		}
	}
	return
}

// Exports at 23.976, 25, 29.97 and 59.94 fps carry the exact rational rate and exactly the
// frames of the timeline; sound and picture have the same length.
func TestExportRationalRates(t *testing.T) {
	needFFmpeg(t)
	r := render.New(media.NewCache())
	for _, rate := range []model.Rate{model.NTSC24, model.Rate25, model.NTSC30, model.NTSC60} {
		t.Run(rate.String(), func(t *testing.T) {
			const n = 72
			p := rateProject(t, rate, n)
			path := filepath.Join(t.TempDir(), "out.mp4")
			o := Options{Format: FormatMP4, Path: path, Width: 160, Height: 90, Quality: QualityLow, IncludeAudio: true}
			if err := Run(context.Background(), p, r, o, nil); err != nil {
				t.Fatal(err)
			}
			v, a := probeRate(t, path)
			if v.rate != rate.Fraction()+map[bool]string{true: "", false: "/1"}[rate.Den != 1] {
				t.Errorf("r_frame_rate %q, want %s", v.rate, rate.Fraction())
			}
			if v.frames != n {
				t.Errorf("%d video frames, want %d", v.frames, n)
			}
			want := rate.FramesToSeconds(n)
			if math.Abs(v.dur-want) > 0.002 {
				t.Errorf("video %.4f s, want %.4f s", v.dur, want)
			}
			if math.Abs(a.dur-want) > 0.05 {
				t.Errorf("audio %.4f s, want %.4f s", a.dur, want)
			}
		})
	}
}

// Options.FPS converts the rendered stream: 25 fps project to 29.97 and 50 fps output.
func TestExportFPSOverride(t *testing.T) {
	needFFmpeg(t)
	r := render.New(media.NewCache())
	p := rateProject(t, model.Rate25, 50) // 2 s
	for _, c := range []struct {
		fps    model.Rate
		rate   string
		frames int
	}{{model.NTSC30, "30000/1001", 60}, {model.Rate50, "50/1", 100}, {model.NTSC24, "24000/1001", 48}} {
		path := filepath.Join(t.TempDir(), "out.mp4")
		o := Options{Format: FormatMP4, Path: path, Width: 160, Height: 90, Quality: QualityLow, FPS: c.fps}
		if err := Run(context.Background(), p, r, o, nil); err != nil {
			t.Fatal(err)
		}
		v, _ := probeRate(t, path)
		if v.rate != c.rate || v.frames < c.frames-1 || v.frames > c.frames+1 {
			t.Errorf("%v: rate %s frames %d, want %s and about %d", c.fps, v.rate, v.frames, c.rate, c.frames)
		}
	}
	bad := Options{Format: FormatMP4, Path: filepath.Join(t.TempDir(), "x.mp4"), Width: 160, Height: 90, FPS: model.Rate{Num: 300, Den: 1}}
	if err := Validate(p, bad); err == nil || !strings.Contains(err.Error(), "Export-Bildrate") {
		t.Errorf("rate above 240 fps: %v", err)
	}
}

func TestExportGIFRates(t *testing.T) {
	needFFmpeg(t)
	r := render.New(media.NewCache())
	p := rateProject(t, model.NTSC30, 60) // about 2 s
	path := filepath.Join(t.TempDir(), "out.gif")
	o := Options{Format: FormatGIF, Path: path, Width: 160, Height: 90, Quality: QualityLow}
	if err := Run(context.Background(), p, r, o, nil); err != nil {
		t.Fatal(err)
	}
	if v, _ := probeRate(t, path); v.frames < 28 || v.frames > 32 {
		t.Errorf("GIF has %d frames, want about 30 (15 fps for 2 s)", v.frames)
	}
	if OutputRate(o, model.NTSC30) != model.IntRate(15) || OutputRate(Options{Format: FormatMP4}, model.NTSC30) != model.NTSC30 ||
		OutputRate(Options{Format: FormatGIF, FPS: model.NTSC24}, model.Rate25) != model.NTSC24 {
		t.Error("OutputRate")
	}
}

// Odd sizes are rounded down to even ones and the progress carries the note.
func TestExportRoundsOddSizes(t *testing.T) {
	needFFmpeg(t)
	r := render.New(media.NewCache())
	p := rateProject(t, model.Rate25, 10)
	path := filepath.Join(t.TempDir(), "odd.mp4")
	o := Options{Format: FormatMP4, Path: path, Width: 161, Height: 91, Quality: QualityLow}
	note := ""
	if err := Run(context.Background(), p, r, o, func(pr Progress) {
		if pr.Note != "" {
			note = pr.Note
		}
	}); err != nil {
		t.Fatal(err)
	}
	if v, _ := probeRate(t, path); v.w != 160 || v.h != 90 {
		t.Errorf("size %dx%d", v.w, v.h)
	}
	if !strings.Contains(note, "160 × 90") {
		t.Errorf("note %q", note)
	}
}

func TestTimelineFramesRational(t *testing.T) {
	// 29.97 -> 15 fps over 10 s: exactly the frames at k/15 s
	got := timelineFrames(0, 300, model.NTSC30, model.IntRate(15))
	if len(got) != 151 || got[0] != 0 || got[1] != 1 || got[2] != 3 || got[150] != 299 {
		t.Errorf("%d frames, first %v last %d", len(got), got[:3], got[len(got)-1])
	}
	if got := timelineFrames(0, 5, model.Rate25, model.NTSC30); len(got) != 5 {
		t.Errorf("a higher output rate keeps every frame: %d", len(got))
	}
	// the duration of the audio mix equals the video: sample counts come from the exact rate
	p := rateProject(t, model.NTSC30, 3000)
	mix, ok := BuildAudioMix(p, 0, 3000, 0, nil)
	if !ok || !strings.Contains(mix.Filter, "end_sample=4804800") {
		t.Errorf("3000 frames at 29.97 are 100.1 s = 4804800 samples: %v\n%s", ok, mix.Filter)
	}
}

func TestGIFRateCapped(t *testing.T) {
	for _, fps := range []model.Rate{model.Rate60, model.NTSC60, model.Rate120, model.IntRate(240)} {
		o := Options{Format: FormatGIF, FPS: fps}
		if got := OutputRate(o, model.Rate25); got != model.IntRate(50) {
			t.Errorf("GIF at %v -> %v, want 50", fps, got)
		}
		if RateNote(o) == "" {
			t.Error("note missing")
		}
	}
	if got := OutputRate(Options{Format: FormatGIF, FPS: model.Rate30}, model.Rate25); got != model.Rate30 || RateNote(Options{Format: FormatGIF, FPS: model.Rate30}) != "" {
		t.Error("30 fps GIF is fine")
	}
	if got := OutputRate(Options{Format: FormatMP4, FPS: model.Rate120}, model.Rate25); got != model.Rate120 {
		t.Error("MP4 120 fps")
	}
}

func TestPoolShrinksForHugeFrames(t *testing.T) {
	if poolFor(1920, 1080) != workers() {
		t.Error("HD uses the full pool")
	}
	if poolFor(16384, 16384) != 1 || poolFor(8192, 8192) > 2 {
		t.Error("huge frames use one worker")
	}
	if MemoryNote(Options{Format: FormatMP4, Width: 8192, Height: 8192}) == "" || MemoryNote(Options{Format: FormatMP4, Width: 1920, Height: 1080}) != "" {
		t.Error("MemoryNote")
	}
}
