package timeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/model"
)

// syncProject: V1 A 0-100, B 100-150; V2 T 120-140; A1 S 160-200.
func syncProject(f *fixture) {
	_ = f.s.Edit("x", func(p *model.Project) error {
		_ = p.PlaceClip(p.Tracks[1].ID, model.NewClip(model.KindTitle, "T", 120, 20))
		return p.PlaceClip(p.Tracks[2].ID, model.NewClip(model.KindAudio, "S", 160, 40))
	})
}

func TestRippleDeleteWithoutSyncShiftsOnlyItsTrack(t *testing.T) {
	f := setup(t, 1000, 300)
	syncProject(f)
	a := f.track("V1").Clips[0].ID
	q := f.s.Project.Clone()
	if err := RippleDelete(q, []string{a}, false); err != nil {
		t.Fatal(err)
	}
	clip := func(p *model.Project, name string) model.Clip {
		for _, tr := range p.Tracks {
			for _, c := range tr.Clips {
				if c.Name == name {
					return c
				}
			}
		}
		t.Fatalf("no clip %s", name)
		return model.Clip{}
	}
	if clip(q, "B").Start != 0 || clip(q, "T").Start != 120 || clip(q, "S").Start != 160 {
		t.Fatal("only V1 should close the gap")
	}
	q = f.s.Project.Clone()
	if err := RippleDelete(q, []string{a}, true); err != nil {
		t.Fatal(err)
	}
	if clip(q, "B").Start != 0 || clip(q, "T").Start != 20 || clip(q, "S").Start != 60 {
		t.Fatal("synced ripple should move every track")
	}
}

func TestRippleTrimAndMenuFollowSyncLock(t *testing.T) {
	f := setup(t, 1000, 300)
	syncProject(f)
	f.tl.SetRippleSync(false)
	a := f.track("V1").Clips[0].ID
	q := f.s.Project.Clone()
	if err := rippleTrimSync(q, a, false, -30, f.tl.RippleSync()); err != nil {
		t.Fatal(err)
	}
	tr, _ := q.Track(f.track("V2").ID)
	if tr.Clips[0].Start != 120 {
		t.Fatal("ripple trim without sync lock moved V2")
	}
	// "Lücke schließen" in the clip menu uses the setting
	m := f.tl.clipMenu(f.track("V1").Clips[0])
	for _, it := range m.Items {
		if it.Label == "Lücke schließen" {
			it.Action()
		}
	}
	if f.track("V1").Clips[0].Name != "B" || f.track("V1").Clips[0].Start != 0 || f.track("V2").Clips[0].Start != 120 {
		t.Fatal("menu ripple delete should keep V2")
	}
}

func TestClipMenuHookAddsEntries(t *testing.T) {
	f := setup(t, 1000, 300)
	ran := false
	f.tl.ClipMenu = func(c model.Clip) []*fyne.MenuItem {
		return []*fyne.MenuItem{fyne.NewMenuItem("Kopieren", func() { ran = c.Name == "A" })}
	}
	m := f.tl.clipMenu(f.track("V1").Clips[0])
	last := m.Items[len(m.Items)-1]
	if last.Label != "Kopieren" || !m.Items[len(m.Items)-2].IsSeparator {
		t.Fatal("extra entries should follow a separator")
	}
	last.Action()
	if !ran {
		t.Fatal("entry not wired")
	}
}

func TestTrackHeightDefaults(t *testing.T) {
	f := setup(t, 1000, 400)
	before := f.tl.ContentHeight()
	f.tl.SetTrackHeights(80, 40)
	if v, a := f.tl.TrackHeights(); v != 80 || a != 40 {
		t.Fatalf("heights %v/%v", v, a)
	}
	// V1, V2 and A1
	if got := f.tl.ContentHeight(); got != before+2*(80-videoRowH)+(40-audioRowH) {
		t.Fatalf("content height %v", got)
	}
	g := f.tl.geom()
	if g.rows[0].h != 80 || g.rows[2].h != 40 {
		t.Fatalf("rows %+v", g.rows)
	}
	f.tl.SetTrackHeights(1000, 0)
	if v, a := f.tl.TrackHeights(); v != maxRowH || a != audioRowH {
		t.Fatalf("heights should be clamped / default: %v %v", v, a)
	}
}

func TestFollowModes(t *testing.T) {
	f := setup(t, 1000, 300)
	_ = f.s.Edit("x", func(p *model.Project) error {
		return p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Lang", 150, 2000))
	})
	f.tl.SetZoom(10)
	f.tl.SetFollow(FollowOff)
	f.s.Seek(0)
	off := f.tl.offset
	f.s.Seek(1500)
	if f.tl.offset != off {
		t.Fatal("follow off must not scroll")
	}
	f.tl.SetFollow(FollowSmooth)
	f.s.Seek(990)
	f.s.SetPlaying(true)
	f.s.Advance(400 * time.Millisecond) // 10 frames at 25 fps
	if f.s.Playhead != 1000 {
		t.Fatalf("playhead %d", f.s.Playhead)
	}
	g := f.tl.geom()
	if mid := headerW + g.laneW()/2; abs(int(g.frameX(1000)-mid)) > 2 {
		t.Fatalf("smooth follow keeps the playhead centred: %v vs %v", g.frameX(1000), mid)
	}
	f.s.Advance(400 * time.Millisecond)
	if g = f.tl.geom(); abs(int(g.frameX(1010)-(headerW+g.laneW()/2))) > 2 {
		t.Fatal("smooth follow should scroll with every frame")
	}
	f.s.SetPlaying(false)
	f.tl.SetFollow("unbekannt")
	if f.tl.Follow() != FollowPage {
		t.Fatal("unknown modes fall back to paging")
	}
}

func TestHiddenFilmstripLoadsNoFrames(t *testing.T) {
	f := setup(t, 1200, 300)
	path := filepath.Join(t.TempDir(), "v.mov")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &posterFake{}
	f.tl.ld.src = src
	f.tl.posters.src = src
	f.tl.posters.inline = true
	f.tl.SetShowFilmstrip(false)
	f.tl.SetShowWaveforms(false)
	m := model.MediaItem{ID: "v", Name: "v.mov", Path: path, Kind: model.KindVideo, Duration: 40 * time.Second, Width: 1920, Height: 1080}
	_ = f.s.Edit("x", func(p *model.Project) error {
		p.AddMedia(m)
		return p.PlaceClip(p.Tracks[1].ID, p.ClipFromMedia(m, 0, 0))
	})
	f.tl.Refresh()
	if n := len(src.requests()); n != 0 {
		t.Fatalf("hidden filmstrip requested %d frames", n)
	}
	f.tl.SetShowFilmstrip(true)
	if len(src.requests()) == 0 {
		t.Fatal("showing the filmstrip should load frames")
	}
}

func TestDroppedStillsUseStillSeconds(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetStillSeconds(2)
	_ = f.s.Edit("x", func(p *model.Project) error {
		p.AddMedia(model.MediaItem{ID: "img", Name: "Foto.jpg", Kind: model.KindImage, Width: 10, Height: 10})
		return nil
	})
	pos, _ := f.tl.LanePoint(300, f.track("V2").ID)
	if !f.tl.MediaDrop([]string{"img"}, pos) {
		t.Fatal("drop failed")
	}
	if c := f.track("V2").Clips; len(c) != 1 || c[0].Length != 50 {
		t.Fatalf("still should be 2 s long: %+v", c)
	}
}
