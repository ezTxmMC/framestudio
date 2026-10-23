package model

import (
	"errors"
	"testing"
	"time"
)

func proj() (*Project, string) {
	p := NewProject("Test")
	v1 := p.Tracks[0].ID
	_ = p.PlaceClip(v1, NewClip(KindColor, "A", 0, 50))
	_ = p.PlaceClip(v1, NewClip(KindColor, "B", 50, 50))
	return p, v1
}

func names(t *Track) []string {
	var n []string
	for _, c := range t.Clips {
		n = append(n, c.Name)
	}
	return n
}

func TestNewProjectTracks(t *testing.T) {
	p := NewProject("x")
	if len(p.Tracks) != 3 || p.Tracks[0].Name != "V1" || p.Tracks[1].Name != "V2" || p.Tracks[2].Name != "A1" {
		t.Fatalf("unexpected tracks %+v", p.Tracks)
	}
	p.AddTrack(TrackVideo)
	if p.Tracks[2].Name != "V3" || p.Tracks[3].Kind != TrackAudio {
		t.Fatal("video tracks must stay ahead of audio tracks")
	}
}

func TestOverwriteSplitsUnderlyingClip(t *testing.T) {
	p, v1 := proj()
	c := NewClip(KindColor, "X", 20, 10)
	if err := p.PlaceClip(v1, c); err != nil {
		t.Fatal(err)
	}
	tr := &p.Tracks[0]
	if len(tr.Clips) != 4 {
		t.Fatalf("expected A|X|A|B, got %v", names(tr))
	}
	if tr.Clips[2].Start != 30 || tr.Clips[2].Length != 20 || tr.Clips[2].SourceIn != 30 {
		t.Fatalf("right remainder wrong: %+v", tr.Clips[2])
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMoveAcrossTracksAndKinds(t *testing.T) {
	p, _ := proj()
	a := p.Tracks[0].Clips[0].ID
	if err := p.MoveClip(a, p.Tracks[1].ID, 10); err != nil {
		t.Fatal(err)
	}
	if len(p.Tracks[0].Clips) != 1 || p.Tracks[1].Clips[0].Start != 10 {
		t.Fatal("move to V2 failed")
	}
	if err := p.MoveClip(a, p.Tracks[2].ID, 0); err != ErrWrongTrack {
		t.Fatalf("color clip on audio track should fail, got %v", err)
	}
}

func TestTrimAndSplit(t *testing.T) {
	p, _ := proj()
	b := p.Tracks[0].Clips[1].ID
	if err := p.TrimClip(b, 40, 60); err != nil { // extend left into A
		t.Fatal(err)
	}
	if p.Tracks[0].Clips[0].Length != 40 {
		t.Fatalf("A should be trimmed by overwrite, got %d", p.Tracks[0].Clips[0].Length)
	}
	right, err := p.SplitClip(b, 70)
	if err != nil {
		t.Fatal(err)
	}
	c, _, _ := p.Clip(right)
	if c.Start != 70 || c.Length != 30 || c.SourceIn != 30 {
		t.Fatalf("bad right part %+v", c)
	}
}

func TestMediaTrimLimit(t *testing.T) {
	p := NewProject("m")
	id := p.AddMedia(MediaItem{Name: "v", Kind: KindVideo, Duration: 4 * time.Second})
	m, _ := p.MediaByID(id)
	c := p.ClipFromMedia(*m, 0, 50)
	if c.Length != 100 {
		t.Fatalf("expected 100 frames, got %d", c.Length)
	}
	_ = p.PlaceClip(p.Tracks[0].ID, c)
	if err := p.TrimClip(c.ID, 0, 101); err == nil {
		t.Fatal("must not extend past media end")
	}
	_ = p.TrimClip(c.ID, 10, 90)
	if err := p.TrimClip(c.ID, 5, 95); err != nil {
		t.Fatalf("extending back to source start should work: %v", err)
	}
	if err := p.TrimClip(c.ID, 0, 100); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := p.Clip(c.ID); got.SourceIn != 0 {
		t.Fatalf("source in %d", got.SourceIn)
	}
	_ = p.RemoveMedia(id)
	if len(p.Tracks[0].Clips) != 0 {
		t.Fatal("removing media must remove its clips")
	}
}

func TestRippleDeleteAndEditPoints(t *testing.T) {
	p, _ := proj()
	_ = p.DeleteClip(p.Tracks[0].Clips[0].ID, true)
	if p.Tracks[0].Clips[0].Start != 0 || p.Duration() != 50 {
		t.Fatal("ripple delete should close the gap")
	}
	if pts := p.EditPoints(); len(pts) != 2 || pts[1] != 50 {
		t.Fatalf("edit points %v", pts)
	}
}

func TestFadeAndTimecode(t *testing.T) {
	c := NewClip(KindColor, "f", 0, 20)
	c.FadeIn, c.FadeOut = 5, 5
	if c.FadeFactor(0) != 0 || c.FadeFactor(10) != 1 || c.FadeFactor(19) != 0 || c.FadeFactor(2) != 0.4 {
		t.Fatalf("fade %v %v %v", c.FadeFactor(0), c.FadeFactor(10), c.FadeFactor(19))
	}
	if Timecode(25*3661+3, Rate25, false) != "01:01:01:03" {
		t.Fatal(Timecode(25*3661+3, Rate25, false))
	}
}

func TestCloneIsDeep(t *testing.T) {
	p, _ := proj()
	c := p.Clone()
	c.Tracks[0].Clips[0].Name = "changed"
	if p.Tracks[0].Clips[0].Name != "A" {
		t.Fatal("clone shares memory")
	}
}

func TestAddTrackPicksFreeName(t *testing.T) {
	p := NewProject("x") // V1, V2, A1
	if err := p.RemoveTrack(p.Tracks[0].ID); err != nil {
		t.Fatal(err)
	}
	p.AddTrack(TrackVideo)
	p.AddTrack(TrackVideo)
	seen := map[string]bool{}
	for _, tr := range p.Tracks {
		if seen[tr.Name] {
			t.Fatalf("duplicate track name %s in %v", tr.Name, p.Tracks)
		}
		seen[tr.Name] = true
	}
	if !seen["V1"] || !seen["V3"] {
		t.Fatalf("expected V1 and V3, got %v", seen)
	}
}

func TestLockedTrackProtectsRemoval(t *testing.T) {
	p, v1 := proj()
	m := MediaItem{ID: "m1", Kind: KindImage, Name: "Bild", Path: "/x.png"}
	p.AddMedia(m)
	c := p.ClipFromMedia(m, 200, 25)
	if err := p.PlaceClip(v1, c); err != nil {
		t.Fatal(err)
	}
	p.Tracks[0].Locked = true
	if err := p.RemoveTrack(v1); err != ErrLocked {
		t.Fatalf("RemoveTrack on locked track = %v, want ErrLocked", err)
	}
	if err := p.RemoveMedia("m1"); err != ErrLocked {
		t.Fatalf("RemoveMedia with clip on locked track = %v, want ErrLocked", err)
	}
	if len(p.Media) != 1 || len(p.Tracks[0].Clips) != 3 {
		t.Fatal("refused removal must not change the project")
	}
}

func fadeCurve(p *Project, from, to int) []float64 {
	var out []float64
	for f := from; f < to; f++ {
		g := 0.0
		for _, tr := range p.Tracks {
			if ci := tr.ClipAt(f); ci >= 0 {
				g = tr.Clips[ci].FadeFactor(f)
			}
		}
		out = append(out, g)
	}
	return out
}

func TestSplitKeepsFadesContinuous(t *testing.T) {
	for _, cut := range []int{1, 20, 49, 50, 51, 80, 99} {
		p := NewProject("x")
		v1 := p.Tracks[0].ID
		c := NewClip(KindColor, "A", 0, 100)
		c.FadeIn, c.FadeOut = 50, 50
		_ = p.PlaceClip(v1, c)
		want := fadeCurve(p, 0, 100)
		if _, err := p.SplitClip(c.ID, cut); err != nil {
			t.Fatal(err)
		}
		got := fadeCurve(p, 0, 100)
		for i := range want {
			if d := got[i] - want[i]; d > 1e-9 || d < -1e-9 {
				t.Fatalf("cut %d: frame %d gain %.3f, want %.3f", cut, i, got[i], want[i])
			}
		}
		// a second split of a part keeps it continuous too
		if r := p.Tracks[0].Clips[len(p.Tracks[0].Clips)-1]; r.Length > 2 {
			_, _ = p.SplitClip(r.ID, r.Start+r.Length/2)
			got = fadeCurve(p, 0, 100)
			for i := range want {
				if d := got[i] - want[i]; d > 1e-9 || d < -1e-9 {
					t.Fatalf("cut %d+: frame %d gain %.3f, want %.3f", cut, i, got[i], want[i])
				}
			}
		}
	}
}

func TestOverwriteInsideFadeKeepsCurve(t *testing.T) {
	p := NewProject("x")
	v1 := p.Tracks[0].ID
	c := NewClip(KindColor, "A", 0, 100)
	c.FadeIn, c.FadeOut = 40, 40
	_ = p.PlaceClip(v1, c)
	want := fadeCurve(p, 0, 100)
	_ = p.PlaceClip(v1, NewClip(KindColor, "X", 30, 40))
	got := fadeCurve(p, 0, 100)
	for _, f := range []int{0, 10, 29, 70, 80, 99} {
		if d := got[f] - want[f]; d > 1e-9 || d < -1e-9 {
			t.Fatalf("frame %d gain %.3f, want %.3f", f, got[f], want[f])
		}
	}
}

func TestTrimClampsFades(t *testing.T) {
	p := NewProject("x")
	v1 := p.Tracks[0].ID
	c := NewClip(KindColor, "A", 0, 100)
	c.FadeIn, c.FadeOut = 60, 60
	_ = p.PlaceClip(v1, c)
	if err := p.TrimClip(c.ID, 0, 30); err != nil {
		t.Fatal(err)
	}
	got, _, _ := p.Clip(c.ID)
	if got.FadeIn != 30 || got.FadeOut != 30 {
		t.Fatalf("fades %d/%d, want 30/30", got.FadeIn, got.FadeOut)
	}
}

func TestPlaceMediaSkipsLockedTracks(t *testing.T) {
	p := NewProject("x") // V1, V2, A1
	a2 := p.AddTrack(TrackAudio)
	a2ID := a2.ID
	m := MediaItem{ID: "v", Name: "Strand", Kind: KindVideo, HasAudio: true, Duration: 2 * time.Second, Path: "/v.mp4"}
	p.AddMedia(m)
	p.Tracks[0].Locked = true // V1
	p.Tracks[2].Locked = true // A1
	id, err := p.PlaceMedia(m, 0, 125, p.Tracks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ti, _ := p.Clip(id); p.Tracks[ti].Name != "V2" {
		t.Fatalf("picture on %s, want V2", p.Tracks[ti].Name)
	}
	if tr, _ := p.Track(a2ID); len(tr.Clips) != 1 || tr.Clips[0].Kind != KindAudio {
		t.Fatalf("sound must go to the free A2: %+v", tr.Clips)
	}
	// all audio tracks locked: nothing is placed and the error says why
	tr, _ := p.Track(a2ID)
	tr.Locked = true
	before := len(p.Tracks[1].Clips)
	if _, err := p.PlaceMedia(m, 100, 125, ""); err == nil || !errors.Is(err, ErrLocked) {
		t.Fatalf("got %v, want a locked-tracks error", err)
	}
	if len(p.Tracks[1].Clips) != before {
		t.Fatal("failed placement must not leave the picture behind")
	}
	// no audio track at all: one is created
	q := NewProject("y")
	q.RemoveTrack(q.Tracks[2].ID)
	q.AddMedia(m)
	if _, err := q.PlaceMedia(m, 0, 125, ""); err != nil || len(q.TrackIndices(TrackAudio)) != 1 {
		t.Fatalf("audio track not created: %v", err)
	}
}

func TestGroupMoveAndDelete(t *testing.T) {
	p, v1 := proj() // A[0,50) B[50,100) on V1
	a, b := p.Tracks[0].Clips[0].ID, p.Tracks[0].Clips[1].ID
	if err := p.MoveClips([]string{a, b}, 10, 1); err != nil {
		t.Fatal(err)
	}
	if len(p.Tracks[0].Clips) != 0 || len(p.Tracks[1].Clips) != 2 || p.Tracks[1].Clips[0].Start != 10 || p.Tracks[1].Clips[1].Start != 60 {
		t.Fatalf("group move wrong: %+v", p.Tracks[1].Clips)
	}
	if err := p.MoveClips([]string{a}, 0, 1); err != ErrInvalidEdit {
		t.Fatalf("no V3: want ErrInvalidEdit, got %v", err)
	}
	_ = p.PlaceClip(v1, NewClip(KindColor, "C", 200, 10))
	if err := p.DeleteClips([]string{a, b}, true); err != nil {
		t.Fatal(err)
	}
	if len(p.Tracks[1].Clips) != 0 || p.Tracks[0].Clips[0].Start != 100 {
		t.Fatalf("ripple must close the gap on every track: C at %d, want 100", p.Tracks[0].Clips[0].Start)
	}
}

func TestRippleDeleteKeepsTracksInSync(t *testing.T) {
	p := NewProject("x") // V1, V2, A1
	v1, a1 := p.Tracks[0].ID, p.Tracks[2].ID
	m := MediaItem{ID: "m", Kind: KindVideo, HasAudio: true, Duration: 10 * time.Second, Path: "/m.mp4"}
	p.AddMedia(m)
	a := NewClip(KindColor, "A", 0, 100)
	_ = p.PlaceClip(v1, a)
	b, _ := p.PlaceMedia(m, 100, 125, v1) // B 100..350 with sound on A1
	sound := p.LinkedClips(b)
	if len(sound) != 1 {
		t.Fatalf("linked sound %v", sound)
	}
	if err := p.DeleteClips([]string{a.ID}, true); err != nil {
		t.Fatal(err)
	}
	bc, _, _ := p.Clip(b)
	sc, _, _ := p.Clip(sound[0])
	if bc.Start != 0 || sc.Start != 0 {
		t.Fatalf("picture at %d, sound at %d; both must move to 0", bc.Start, sc.Start)
	}
	// a clip in the gap on another track blocks the ripple and nothing changes
	q := p.Clone()
	_ = q.PlaceClip(a1, NewClip(KindAudio, "Musik", 400, 50))
	pic := NewClip(KindColor, "P", 300, 100)
	_ = q.PlaceClip(v1, pic)
	_ = q.PlaceClip(v1, NewClip(KindColor, "Q", 450, 10))
	// A1 holds sound 0..250 and Musik 400..450; deleting P (300..400) would push Musik onto
	// nothing, but V1's Q moves to 350 and A1's Musik (start 400 >= 400) to 300: no clash
	if err := q.DeleteClips([]string{pic.ID}, true); err != nil {
		t.Fatal(err)
	}
	r := p.Clone()
	_ = r.PlaceClip(a1, NewClip(KindAudio, "Musik", 260, 200)) // spans 300..400
	_ = r.PlaceClip(a1, NewClip(KindAudio, "Ton", 500, 10))
	pic2 := NewClip(KindColor, "P", 300, 100)
	_ = r.PlaceClip(v1, pic2)
	var rb *RippleBlockedError
	if err := r.DeleteClips([]string{pic2.ID}, true); !errors.As(err, &rb) || rb.Track != "A1" {
		t.Fatalf("got %v, want ripple blocked on A1", err)
	}
}

func TestDropSoundGoesToFreeOrPointedTrack(t *testing.T) {
	p := NewProject("x") // V1, V2, A1
	a1 := p.Tracks[2].ID
	a2 := p.AddTrack(TrackAudio).ID
	musik := NewClip(KindAudio, "Musik", 0, 400)
	_ = p.PlaceClip(a1, musik)
	m := MediaItem{ID: "m", Kind: KindVideo, HasAudio: true, Duration: 4 * time.Second, Path: "/m.mp4"}
	p.AddMedia(m)
	// on V1 at 200: A1 is busy with music, so the sound goes to the free A2
	id, err := p.PlaceMedia(m, 200, 125, p.Tracks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if tr, _ := p.Track(a1); len(tr.Clips) != 1 || tr.Clips[0].Length != 400 {
		t.Fatalf("music must stay whole: %+v", tr.Clips)
	}
	if s := p.LinkedClips(id); len(s) != 1 {
		t.Fatal("no sound placed")
	} else if _, ti, _ := p.Clip(s[0]); p.Tracks[ti].ID != a2 {
		t.Fatalf("sound on %s, want A2", p.Tracks[ti].Name)
	}
	// pointing at A1 explicitly overwrites there
	q := NewProject("y")
	_ = q.PlaceClip(q.Tracks[2].ID, NewClip(KindAudio, "Musik", 0, 400))
	q.AddMedia(m)
	id, _ = q.PlaceMediaTo(m, 200, 125, "", q.Tracks[2].ID)
	if s := q.LinkedClips(id); len(s) != 1 {
		t.Fatal("no sound")
	} else if _, ti, _ := q.Clip(s[0]); ti != 2 {
		t.Fatal("sound must go to the pointed track")
	}
	// all audio busy: a new audio track is created
	r := NewProject("z")
	_ = r.PlaceClip(r.Tracks[2].ID, NewClip(KindAudio, "Musik", 0, 400))
	r.AddMedia(m)
	if _, err := r.PlaceMedia(m, 100, 125, ""); err != nil || len(r.TrackIndices(TrackAudio)) != 2 {
		t.Fatalf("want a new audio track, err %v", err)
	}
	if len(r.Tracks[2].Clips) != 1 || r.Tracks[2].Clips[0].Length != 400 {
		t.Fatal("music must not be cut")
	}
	// overwriting a video with sound reuses its sound track
	id1, _ := r.PlaceMedia(m, 500, 125, r.Tracks[0].ID)
	_, _ = r.PlaceMedia(m, 500, 125, r.Tracks[0].ID)
	if c, _, _ := r.Clip(id1); c != nil {
		t.Fatal("old picture should be overwritten")
	}
	if n := len(r.TrackIndices(TrackAudio)); n != 2 {
		t.Fatalf("%d audio tracks, want 2 (sound overwrites its own pair)", n)
	}
}
