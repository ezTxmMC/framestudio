package model

import (
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
