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
	if Timecode(25*3661+3, 25) != "01:01:01:03" {
		t.Fatal(Timecode(25*3661+3, 25))
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
