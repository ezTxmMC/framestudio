package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
)

// ramp is a Source whose left channel encodes the source position: (i mod 16384)/32768.
// The right channel is a constant 0.25. It has frames sample frames.
type ramp struct{ frames int64 }

func (r ramp) Frame(i int64) (float32, float32, bool) {
	if i < 0 || i >= r.frames {
		return 0, 0, false
	}
	return rampValue(i), 0.25, true
}

func rampValue(i int64) float32 { return float32(i%16384) / 32768 }

// project returns a 25 fps project whose A1 (Tracks[2]) holds a clip of media "m"
// (audio, 10 s) at start for length frames.
func project(start, length int) (*model.Project, *model.Clip) {
	p := model.NewProject("t")
	m := model.MediaItem{ID: "m", Name: "m", Path: "/m.wav", Kind: model.KindAudio, Duration: 10 * time.Second}
	p.AddMedia(m)
	c := p.ClipFromMedia(m, start, 0)
	c.Length = length
	if err := p.PlaceClip(p.Tracks[2].ID, c); err != nil {
		panic(err)
	}
	return p, &p.Tracks[2].Clips[0]
}

func plan(p *model.Project) *Plan {
	return BuildPlan(p, func(model.MediaItem) (Source, error) { return ramp{10 * SampleRate}, nil })
}

// render mixes n frames from pos and returns the left/right sums.
func render(pl *Plan, pos int64, step, n int) (l, r []float32) {
	out := make([]float32, 2*n)
	trk := make([][]float32, len(pl.Tracks))
	for i := range trk {
		trk[i] = make([]float32, 2*n)
	}
	pl.Render(pos, step, Window{}, out, trk, n)
	for i := range n {
		l = append(l, out[2*i])
		r = append(r, out[2*i+1])
	}
	return l, r
}

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-5 }

func TestClipBoundariesSampleAccurate(t *testing.T) {
	p, c := project(10, 5) // frames 10..15 = samples 19200..28800
	c.SourceIn = 3         // source sample 5760 plays at 19200
	pl := plan(p)
	l, r := render(pl, 19200-2, 1, 4)
	if l[0] != 0 || l[1] != 0 || r[1] != 0 {
		t.Fatalf("sound before the clip: %v %v", l, r)
	}
	if !near(l[2], rampValue(5760)) || !near(l[3], rampValue(5761)) || r[2] != 0.25 {
		t.Fatalf("first samples %v, want source 5760 on", l)
	}
	l, _ = render(pl, 28800-2, 1, 4)
	if !near(l[1], rampValue(5760+9600-1)) || l[2] != 0 || l[3] != 0 {
		t.Fatalf("clip end %v", l)
	}
}

func TestMuteSoloVolume(t *testing.T) {
	p, c := project(0, 50)
	c.Volume = 0.5
	p.Tracks[2].Volume = 0.8
	a2 := p.AddTrack(model.TrackAudio)
	c2 := p.ClipFromMedia(p.Media[0], 0, 0)
	c2.Length = 50
	if err := p.PlaceClip(a2.ID, c2); err != nil {
		t.Fatal(err)
	}
	_, r := render(plan(p), 100, 1, 1)
	if !near(r[0], 0.25*0.4+0.25) {
		t.Fatalf("A1 0.4 + A2 1.0: got %v", r[0])
	}
	p.Tracks[3].Muted = true
	if _, r = render(plan(p), 100, 1, 1); !near(r[0], 0.1) {
		t.Fatalf("A2 muted: got %v", r[0])
	}
	p.Tracks[3].Muted = false
	p.Tracks[3].Solo = true
	if _, r = render(plan(p), 100, 1, 1); !near(r[0], 0.25) {
		t.Fatalf("A2 solo: got %v", r[0])
	}
	p.Tracks[2].Solo = true
	p.Tracks[2].Muted = true // mute wins over solo, as in export
	if _, r = render(plan(p), 100, 1, 1); !near(r[0], 0.25) {
		t.Fatalf("A1 solo+muted, A2 solo: got %v", r[0])
	}
	pl := plan(p)
	if len(pl.Tracks) != 2 || pl.Tracks[0].Audible || !pl.Tracks[1].Audible {
		t.Fatalf("plan tracks %+v", pl.Tracks)
	}
}

func TestFadesFollowExport(t *testing.T) {
	p, c := project(0, 100) // 4 s = 192000 samples
	c.FadeIn, c.FadeOut = 25, 50
	pl := plan(p)
	_, r := render(pl, 0, 1, 1)
	if r[0] != 0 {
		t.Fatalf("fade-in starts silent, got %v", r[0])
	}
	if _, r = render(pl, 24000, 1, 1); !near(r[0], 0.25*0.5) {
		t.Fatalf("half way into fade-in: %v", r[0])
	}
	if _, r = render(pl, 192000-96000, 1, 1); !near(r[0], 0.25) {
		t.Fatalf("fade-out starts at unity: %v", r[0])
	}
	if _, r = render(pl, 192000-1, 1, 1); !near(r[0], 0.25/96000) {
		t.Fatalf("last sample: %v", r[0])
	}

	// Split inside the fades: the parts continue the ramps (FadeInFrom / FadeOutTo).
	p, c = project(0, 100)
	c.FadeIn, c.FadeOut = 50, 50
	id := c.ID
	right, err := p.SplitClip(id, 30)
	if err != nil {
		t.Fatal(err)
	}
	pl = plan(p)
	_, r = render(pl, 30*1920, 1, 1)
	if !near(r[0], 0.25*0.6) { // 30/50 of the fade-in
		t.Fatalf("right part starts at %v, want %v", r[0], 0.25*0.6)
	}
	_, r = render(pl, 30*1920-1, 1, 1) // the left part ends inside its partial fade-in
	want := 0.25 * float64(30*1920-1) / 96000
	if math.Abs(float64(r[0])-want) > 1e-5 {
		t.Fatalf("left part ends at %v, want %v", r[0], want)
	}
	rc, _, _ := p.Clip(right)
	if rc.FadeInFrom == 0 {
		t.Fatal("split must carry FadeInFrom")
	}
}

func TestRenderStepsAndWindow(t *testing.T) {
	p, _ := project(0, 100)
	pl := plan(p)
	l, _ := render(pl, 1000, -1, 3)
	if !near(l[0], rampValue(1000)) || !near(l[2], rampValue(998)) {
		t.Fatalf("reverse %v", l)
	}
	l, _ = render(pl, 1000, 2, 2)
	if !near(l[0], (rampValue(1000)+rampValue(1001))/2) || !near(l[1], (rampValue(1002)+rampValue(1003))/2) {
		t.Fatalf("double speed %v", l)
	}
	out := make([]float32, 8)
	trk := [][]float32{make([]float32, 8)}
	pl.Render(1000, 1, Window{0, 1002}, out, trk, 4)
	if out[2] == 0 || out[4] != 0 || out[6] != 0 {
		t.Fatalf("window must stop the sound at Out: %v", out)
	}
}

func TestSkipsNonAudioClips(t *testing.T) {
	p := model.NewProject("t")
	v := model.MediaItem{ID: "v", Path: "/v.mp4", Kind: model.KindVideo, Duration: time.Second}
	p.AddMedia(v)
	c := p.ClipFromMedia(v, 0, 0)
	_ = p.PlaceClip(p.Tracks[0].ID, c)
	ac := c
	ac.ID, ac.Kind = model.NewID(), model.KindAudio
	_ = p.PlaceClip(p.Tracks[2].ID, ac) // video without sound: silent, like export
	calls := 0
	pl := BuildPlan(p, func(model.MediaItem) (Source, error) { calls++; return ramp{SampleRate}, nil })
	if calls != 0 || pl.Audible(0, SampleRate) {
		t.Fatal("video without sound must not play")
	}
	p.Media[0].HasAudio = true
	pl = BuildPlan(p, func(model.MediaItem) (Source, error) { calls++; return ramp{SampleRate}, nil })
	if calls != 1 || !pl.Audible(0, SampleRate) {
		t.Fatal("video sound plays")
	}
}

func TestSoftClip(t *testing.T) {
	for _, x := range []float32{0, 0.5, -0.89, 0.9} {
		if SoftClip(x) != x {
			t.Fatalf("SoftClip(%v) changed a normal level", x)
		}
	}
	prev := float32(0.9)
	for x := float32(0.91); x < 10; x += 0.05 {
		y := SoftClip(x)
		if y < prev || y > 1 || SoftClip(-x) != -y {
			t.Fatalf("SoftClip(%v) = %v", x, y)
		}
		prev = y
	}
}

// TestMatchesFFmpegExport mixes the same project with export's ffmpeg filter graph and
// with the engine and compares the samples, at frame rates whose frames are not a whole
// number of milliseconds (24, 30 fps) too: cuts must line up to the sample.
func TestMatchesFFmpegExport(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	wav := filepath.Join(dir, "tone.wav")
	if out, err := exec.Command(ff, "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=330:duration=4:sample_rate=48000",
		"-af", "volume=0.5", "-ac", "2", wav).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	cache := NewCache(nil, nil, 0)
	defer cache.Close()
	pcm, err := cache.Get(wav)
	if err != nil {
		t.Fatal(err)
	}
	<-pcm.Done()
	if pcm.Err() != nil {
		t.Fatal(pcm.Err())
	}
	for _, fps := range []int{25, 24, 30} {
		t.Run(fmt.Sprint(fps), func(t *testing.T) {
			p := model.NewProject("t")
			p.Settings.FPS = fps
			m := model.MediaItem{ID: "m", Name: "tone", Path: wav, Kind: model.KindAudio, Duration: 4 * time.Second}
			p.AddMedia(m)
			a2 := p.AddTrack(model.TrackAudio)
			c := p.ClipFromMedia(m, 5, 0)
			c.Length, c.SourceIn, c.FadeIn, c.FadeOut, c.Volume = 60, 10, 20, 30, 0.7
			_ = p.PlaceClip(p.Tracks[2].ID, c)
			right, _ := p.SplitClip(c.ID, 15) // inside the fade-in
			_, _ = p.SplitClip(right, 50)     // inside the fade-out
			c2 := p.ClipFromMedia(m, 20, 0)
			c2.Length, c2.Volume = 40, 0.6
			_ = p.PlaceClip(a2.ID, c2)
			a2.Volume = 0.9
			// a plain cut without fades: the two halves must join without a gap
			a3 := p.AddTrack(model.TrackAudio)
			c3 := p.ClipFromMedia(m, 0, 0)
			c3.Length = 80
			_ = p.PlaceClip(a3.ID, c3)
			_, _ = p.SplitClip(c3.ID, 37)

			from, to := 3, 80
			mix, ok := export.BuildAudioMix(p, from, to, 0, nil)
			if !ok {
				t.Fatal("no mix")
			}
			args := []string{"-nostdin", "-loglevel", "error"}
			for _, in := range mix.Inputs {
				args = append(args, "-i", in)
			}
			args = append(args, "-filter_complex", mix.Filter, "-map", "[aout]", "-f", "f32le", "-ar", "48000", "-ac", "2", "-")
			raw, err := exec.Command(ff, args...).Output()
			if err != nil {
				t.Fatal(err)
			}
			want := make([]float32, len(raw)/4)
			_ = binary.Read(bytes.NewReader(raw), binary.LittleEndian, want)

			pl := BuildPlan(p, func(model.MediaItem) (Source, error) { return pcm, nil })
			n := len(want) / 2
			start := FramesToSamples(from, fps)
			got, gotR := render(pl, start, 1, n)
			if want := FramesToSamples(to, fps) - start; int64(n) != want {
				t.Fatalf("ffmpeg rendered %d frames, want %d", n, want)
			}
			worst, at := 0.0, 0
			for i := range n {
				for _, d := range []float64{float64(got[i] - want[2*i]), float64(gotR[i] - want[2*i+1])} {
					if math.Abs(d) > worst {
						worst, at = math.Abs(d), i
					}
				}
			}
			t.Logf("max deviation from export %.6f at sample %d", worst, at)
			if worst > 2e-3 {
				if os.Getenv("FS_DUMP") != "" {
					for i := max(0, at-5); i < min(n, at+5); i++ {
						t.Logf("%d: got %.5f want %.5f", i, got[i], want[2*i])
					}
				}
				t.Fatalf("engine mix deviates from export by %.4f", worst)
			}
		})
	}
}
