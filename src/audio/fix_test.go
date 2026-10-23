package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
)

// twoFileProject has a clip of /m.wav on A1 and of /m2.wav on A2, 2 s each.
func twoFileProject() *model.Project {
	p := model.NewProject("t")
	a2 := p.AddTrack(model.TrackAudio)
	for i, path := range []string{"/m.wav", "/m2.wav"} {
		m := model.MediaItem{ID: path, Name: path, Path: path, Kind: model.KindAudio, Duration: 2 * time.Second}
		p.AddMedia(m)
		c := p.ClipFromMedia(m, 0, 0)
		tr := p.Tracks[2].ID
		if i == 1 {
			tr = a2.ID
		}
		if err := p.PlaceClip(tr, c); err != nil {
			panic(err)
		}
	}
	return p
}

func waitPlan(t *testing.T, v *Voice, before *Plan) *Plan {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if pl := v.Plan(); pl != nil && pl != before && decoded(pl) {
			return pl
		}
		if time.Now().After(deadline) {
			t.Fatal("plan not ready")
		}
		time.Sleep(time.Millisecond)
	}
}

// Audio a live plan plays must not be evicted when the timeline holds more audio than the
// budget: otherwise every edit decodes a file again and its clips play silence meanwhile.
func TestPlanAudioStaysPinnedOverBudget(t *testing.T) {
	fm := newFakeMedia(2 * SampleRate) // two chunks per file
	cache := NewCache(fm.decode, fm.stat, 3*chunkFrames*bytesPerFrame)
	e := New(Config{Clock: &fakeClock{t: time.Unix(1, 0)}, Open: func() (Output, error) { return &fakeOut{}, nil }, Cache: cache})
	defer e.Close()
	v := e.AddVoice(nil)
	p := twoFileProject()
	v.SetProject(p.Clone())
	pl := waitPlan(t, v, nil)
	for i := range 4 { // fader moves
		p.Tracks[2].Volume = 0.5 + 0.1*float64(i)
		v.SetProject(p.Clone())
		pl = waitPlan(t, v, pl)
	}
	if fm.count("/m.wav") != 1 || fm.count("/m2.wav") != 1 {
		t.Fatalf("edits decoded again: m %d, m2 %d", fm.count("/m.wav"), fm.count("/m2.wav"))
	}
	for _, tr := range pl.Tracks {
		pcm := tr.Clips[0].Source.(*PCM)
		if pcm.Frames() != 2*SampleRate {
			t.Fatalf("track %s has %d of %d frames", tr.ID, pcm.Frames(), 2*SampleRate)
		}
	}
	if cache.Used() != 4*chunkFrames*bytesPerFrame {
		t.Fatalf("used %d must count every pinned byte", cache.Used())
	}
	// once the plan no longer uses a file, the budget applies again
	p.Tracks[3].Clips = nil
	v.SetProject(p.Clone())
	waitPlan(t, v, pl)
	deadline := time.Now().Add(2 * time.Second)
	for cache.Used() > 3*chunkFrames*bytesPerFrame {
		if time.Now().After(deadline) {
			t.Fatalf("unpinned audio not evicted: used %d", cache.Used())
		}
		time.Sleep(time.Millisecond)
	}
}

// Deleting the last clip of a file keeps its decoded audio, so Undo plays at once.
func TestDeletedClipKeepsAudioForUndo(t *testing.T) {
	r := newRig(t)
	clips := r.p.Tracks[2].Clips
	r.p.Tracks[2].Clips = nil
	r.setProject()
	if keys := r.e.Cache().Keys(); len(keys) != 1 {
		t.Fatalf("decoded audio dropped on delete: %q", keys)
	}
	r.p.Tracks[2].Clips = clips // undo
	r.setProject()
	if n := r.fm.count("/m.wav"); n != 1 {
		t.Fatalf("undo decoded the file again (%d decodes)", n)
	}
}

// Removing a voice (closing a project tab) stops its decodes and frees its audio while
// other voices go on.
func TestRemoveVoiceReleasesAudio(t *testing.T) {
	r := newRig(t)
	r.fm.mu.Lock()
	r.fm.block["/other.wav"] = true
	r.fm.mu.Unlock()
	v2 := r.e.AddVoice(nil)
	p2, _ := project(0, 100)
	p2.Media[0].Path = "/other.wav"
	v2.SetProject(p2)
	deadline := time.Now().Add(5 * time.Second)
	for len(r.e.Cache().Keys()) != 2 || r.fm.count("/other.wav") != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("second voice not decoding: %q", r.e.Cache().Keys())
		}
		time.Sleep(time.Millisecond)
	}
	r.e.RemoveVoice(v2)
	for len(r.e.Cache().Keys()) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("removed voice keeps its audio: %q", r.e.Cache().Keys())
		}
		time.Sleep(time.Millisecond)
	}
	if k := r.e.Cache().Keys()[0]; k != CacheKey("/m.wav", 0, time.Unix(1, 0)) {
		t.Fatalf("the other voice's audio must stay, have %q", k)
	}
}

// A file whose audio stream starts after its video (start_time 0.5 s) must play its sound
// at the timestamps export and the picture use, not shifted to time 0.
func TestDecoderHonoursAudioStartOffset(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	mov := filepath.Join(dir, "late.mov")
	if out, err := exec.Command(ff, "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=64x36:rate=25:duration=3",
		"-itsoffset", "0.5", "-f", "lavfi", "-i", "aevalsrc=t/4|t/4:s=48000:d=2.5",
		"-map", "0:v", "-map", "1:a", "-c:v", "mpeg4", "-c:a", "pcm_s16le", mov).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	c := NewCache(nil, nil, 0)
	defer c.Close()
	pcm, err := c.Get(mov)
	if err != nil {
		t.Fatal(err)
	}
	<-pcm.Done()
	if pcm.Err() != nil {
		t.Fatal(pcm.Err())
	}
	// at file time 1.0 s the ramp has run for 0.5 s: 0.5/4 = 0.125
	l, _, ok := pcm.Frame(SampleRate)
	if !ok || math.Abs(float64(l)-0.125) > 0.01 {
		t.Fatalf("sample at 1 s = %.4f, want 0.125 (audio starts at 0.5 s)", l)
	}
	if l, _, _ := pcm.Frame(SampleRate / 4); math.Abs(float64(l)) > 0.002 {
		t.Fatalf("before the audio starts it must be silent, got %.4f", l)
	}

	// and the engine matches export for a clip that starts 1 s into the file
	p := model.NewProject("t")
	m := model.MediaItem{ID: "v", Name: "late", Path: mov, Kind: model.KindVideo, HasAudio: true, Duration: 3 * time.Second}
	p.AddMedia(m)
	cl := p.ClipFromMedia(m, 0, 0)
	cl.Kind, cl.SourceIn, cl.Length = model.KindAudio, 25, 25
	_ = p.PlaceClip(p.Tracks[2].ID, cl)
	mix, ok := export.BuildAudioMix(p, 0, 25, 0, nil)
	if !ok {
		t.Fatal("no mix")
	}
	args := []string{"-nostdin", "-loglevel", "error", "-i", mov, "-filter_complex", mix.Filter, "-map", "[aout]", "-f", "f32le", "-ar", "48000", "-ac", "2", "-"}
	raw, err := exec.Command(ff, args...).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := make([]float32, len(raw)/4)
	_ = binary.Read(bytes.NewReader(raw), binary.LittleEndian, want)
	pl := BuildPlan(p, func(model.MediaItem) (Source, error) { return pcm, nil })
	got, _ := render(pl, 0, 1, len(want)/2)
	for _, i := range []int{0, 12000, 24000, 47000} {
		if math.Abs(float64(got[i]-want[2*i])) > 2e-3 {
			t.Fatalf("sample %d: engine %.4f, export %.4f", i, got[i], want[2*i])
		}
	}
}
