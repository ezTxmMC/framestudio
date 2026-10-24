package edit

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/timeline"
	"framestudio-proto/src/ui/viewer"
)

// editOwned are the catalog commands the edit page binds (OWNER "edit page" in ids.go).
func editOwned() []string {
	var ids []string
	for _, in := range command.Catalog {
		id := in.ID
		switch {
		case id == "edit.undo" || id == "edit.redo":
		case strings.HasPrefix(id, "edit."), strings.HasPrefix(id, "clip."), strings.HasPrefix(id, "tool."),
			strings.HasPrefix(id, "timeline."), strings.HasPrefix(id, "marker."),
			id == "view.viewerFullscreen", id == "view.safeArea":
			ids = append(ids, id)
		}
	}
	return ids
}

func run(t *testing.T, f *fixture, id string) {
	t.Helper()
	if !f.reg.Run(id) {
		c, _ := f.reg.Get(id)
		t.Fatalf("%s did not run (available %v, enabled %v)", id, c.Available, c.Enabled)
	}
}

func checked(f *fixture, id string) bool {
	c, _ := f.reg.Get(id)
	return c.Checked
}

func enabled(f *fixture, id string) bool {
	c, _ := f.reg.Get(id)
	return c.Enabled
}

func TestEveryEditCommandIsBound(t *testing.T) {
	f := newFixture(t, basicProject())
	for _, id := range editOwned() {
		if c, _ := f.reg.Get(id); !c.Available {
			t.Errorf("%s not bound", id)
		}
	}
	for _, id := range []string{CmdHideViewer, CmdHideLibrary, CmdHideInspector} {
		c, ok := f.reg.Get(id)
		if !ok || !c.Available || c.Category != "Ansicht" || !c.Toggle || c.Label == "" {
			t.Errorf("%s: %+v", id, c)
		}
	}
	// the palette finds them
	found := false
	for _, c := range f.reg.Available() {
		if c.ID == CmdHideViewer && command.Matches(c, "monitor anzeigen") {
			found = true
		}
	}
	if !found {
		t.Fatal("palette does not offer the viewer toggle")
	}
}

func TestToolCommandsAreChecked(t *testing.T) {
	f := newFixture(t, basicProject())
	if !checked(f, "tool.select") || checked(f, "tool.blade") {
		t.Fatal("select tool should be checked at start")
	}
	run(t, f, "tool.blade")
	if !checked(f, "tool.blade") || checked(f, "tool.select") || f.pg.tl.Tool() != timeline.ToolBlade {
		t.Fatal("blade not active")
	}
	if !f.pg.bar.tools[1].on {
		t.Fatal("toolbar does not show the blade")
	}
	// a toolbar click runs the command through the registry
	test.Tap(f.pg.bar.tools[3].Button)
	if !checked(f, "tool.roll") || f.pg.tl.Tool() != timeline.ToolRoll {
		t.Fatal("toolbar click did not reach the command")
	}
	press(f, fyne.KeyA)
	if !checked(f, "tool.select") {
		t.Fatal("A should select the selection tool")
	}
	run(t, f, "timeline.bladeAll")
	if !checked(f, "timeline.bladeAll") || !f.pg.tl.BladeAllTracks() {
		t.Fatal("blade all not on")
	}
}

func TestCustomKeymapReachesPage(t *testing.T) {
	f := newFixture(t, basicProject())
	f.reg.SetShortcut("tool.blade", "Q")
	if press(f, fyne.KeyB) && f.pg.tool == timeline.ToolBlade {
		t.Fatal("B still selects the blade after remapping")
	}
	if !press(f, fyne.KeyQ) || f.pg.tool != timeline.ToolBlade {
		t.Fatal("Q should select the blade")
	}
	if !strings.Contains(f.pg.bar.tools[1].Tip, "(Q)") {
		t.Fatalf("tooltip shows the old key: %q", f.pg.bar.tools[1].Tip)
	}
	// keyboard-layout aliases dispatch to the command keys
	z := f.pg.zoom
	press(f, fyne.KeyRightBracket)
	if f.pg.zoom <= z {
		t.Fatal("] (+ on German keyboards) should zoom in")
	}
}

func TestSnappingFollowsSettings(t *testing.T) {
	f := newFixture(t, basicProject())
	if !checked(f, "timeline.snapping") || !f.pg.tl.Snapping() {
		t.Fatal("snapping should start on (settings default)")
	}
	run(t, f, "timeline.snapping")
	if settings.Get().Timeline.Snapping || f.pg.tl.Snapping() || checked(f, "timeline.snapping") {
		t.Fatal("toggle should switch snapping off in settings and timeline")
	}
	_ = settings.Update(func(s *settings.Settings) { s.Timeline.Snapping = true })
	if !f.pg.tl.Snapping() || !f.pg.bar.snap.on {
		t.Fatal("settings change did not reach the timeline")
	}
}

func TestClipboardCopyPaste(t *testing.T) {
	f := newFixture(t, basicProject())
	board = clipboard{}
	if enabled(f, "edit.paste") || enabled(f, "edit.copy") {
		t.Fatal("paste and copy need content and a selection")
	}
	title := clipNamed(t, f.s.Project, "Titel")
	color := clipNamed(t, f.s.Project, "Farbe")
	f.s.SelectClips([]string{color.ID, title.ID})
	run(t, f, "edit.copy")
	if !enabled(f, "edit.paste") {
		t.Fatal("paste should be enabled after copy")
	}
	f.s.SelectTrack(f.s.Project.Tracks[1].ID) // V2 is the target of the lowest copied track
	f.s.Seek(200)
	run(t, f, "edit.paste")
	p := f.s.Project
	v := p.TrackIndices(model.TrackVideo)
	if len(v) != 3 {
		t.Fatalf("paste onto V2 needs a V3 for the title: %d video tracks", len(v))
	}
	c2, c3 := p.Tracks[v[1]].Clips, p.Tracks[v[2]].Clips
	if len(c2) != 2 || c2[1].Start != 200 || c2[1].Kind != model.KindColor ||
		len(c3) != 1 || c3[0].Start != 225 || c3[0].Kind != model.KindTitle {
		t.Fatalf("relative offsets or tracks lost: V2 %+v V3 %+v", c2, c3)
	}
	if len(f.s.Selected) != 2 || f.s.Playhead != 300 {
		t.Fatalf("pasted clips should be selected, playhead at their end: %v %d", f.s.Selected, f.s.Playhead)
	}
	if f.s.UndoLabel() != "2 Clips einfügen" {
		t.Fatalf("undo label %q", f.s.UndoLabel())
	}
	// cut removes and keeps them on the clipboard
	f.s.SelectClip(c3[0].ID)
	run(t, f, "edit.cut")
	if len(f.s.Project.Tracks[v[2]].Clips) != 0 || len(board.entries) != 1 {
		t.Fatal("cut should remove the clip and fill the clipboard")
	}
	// duplicate places a copy after the selection on the same track
	f.s.SelectClip(clipNamed(t, f.s.Project, "Farbe").ID)
	run(t, f, "edit.duplicate")
	v1 := f.s.Project.Tracks[0].Clips
	if len(v1) != 2 || v1[1].Start != 100 || v1[1].Length != 100 || f.s.SelectedClip != v1[1].ID {
		t.Fatalf("duplicate: %+v", v1)
	}
}

func TestClipboardPastesAcrossProjects(t *testing.T) {
	a := newFixture(t, demoProject())
	drone := clipNamed(t, a.s.Project, "Strand_Drohne.mp4")
	ids := append([]string{drone.ID}, a.s.Project.LinkedClips(drone.ID)...)
	a.s.SelectClips(ids)
	run(t, a, "edit.copy")
	m, _ := a.s.Project.MediaByID(drone.MediaID)
	m.Path = "/media/Strand_Drohne.mp4"
	board.media[0].Path = m.Path

	// a second project tab with its own page; media join it on paste
	b := newFixture(t, basicProject())
	b.s.Seek(100)
	run(t, b, "edit.paste")
	p := b.s.Project
	if len(p.Media) != 1 || p.Media[0].Path != m.Path {
		t.Fatalf("media item not added: %+v", p.Media)
	}
	var pic, snd *model.Clip
	for ti := range p.Tracks {
		for ci := range p.Tracks[ti].Clips {
			c := &p.Tracks[ti].Clips[ci]
			if c.MediaID == p.Media[0].ID && c.Start == 100 {
				if c.Kind == model.KindAudio {
					snd = c
				} else {
					pic = c
				}
			}
		}
	}
	if pic == nil || snd == nil || pic.Length != drone.Length {
		t.Fatalf("picture and sound should paste at the playhead: %v %v", pic, snd)
	}
	if len(p.LinkedClips(pic.ID)) != 1 {
		t.Fatal("picture and sound should stay linked")
	}
	// pasting again reuses the media item
	b.s.Seek(400)
	run(t, b, "edit.paste")
	if len(b.s.Project.Media) != 1 {
		t.Fatal("the same file must not be added twice")
	}
}

func TestFadesUseSettings(t *testing.T) {
	f := newFixture(t, basicProject())
	_ = settings.Update(func(s *settings.Settings) { s.Timeline.FadeSeconds = 2 })
	f.s.SelectClip(clipNamed(t, f.s.Project, "Titel").ID)
	run(t, f, "clip.fadeIn")
	run(t, f, "clip.fadeOut")
	c := clipNamed(t, f.s.Project, "Titel")
	if c.FadeIn != 50 || c.FadeOut != 50 {
		t.Fatalf("2 s fades at 25 fps should be 50 frames: %d/%d", c.FadeIn, c.FadeOut)
	}
	if !enabled(f, "clip.removeFades") {
		t.Fatal("remove fades should be enabled")
	}
	run(t, f, "clip.removeFades")
	if c = clipNamed(t, f.s.Project, "Titel"); c.FadeIn != 0 || c.FadeOut != 0 {
		t.Fatal("fades not removed")
	}
	// a fade never exceeds the clip
	_ = settings.Update(func(s *settings.Settings) { s.Timeline.FadeSeconds = 10 })
	run(t, f, "clip.fadeIn")
	if c = clipNamed(t, f.s.Project, "Titel"); c.FadeIn != c.Length {
		t.Fatalf("fade %d longer than clip %d", c.FadeIn, c.Length)
	}
}

func TestClipLookCommands(t *testing.T) {
	f := newFixture(t, demoProject())
	inter := clipNamed(t, f.s.Project, "Interview_Anna.mov")
	f.s.SelectClip(inter.ID)
	run(t, f, "clip.color.3")
	if !checked(f, "clip.color.3") || checked(f, "clip.color.0") {
		t.Fatal("colour check marks wrong")
	}
	if !checked(f, "clip.fit.contain") {
		t.Fatal("contain should be checked by default")
	}
	run(t, f, "clip.fit.cover")
	if c := clipNamed(t, f.s.Project, "Interview_Anna.mov"); c.Fit != model.FitCover || !checked(f, "clip.fit.cover") {
		t.Fatal("fit not applied")
	}
	if enabled(f, "clip.resetOpacity") {
		t.Fatal("opacity is already full")
	}
	f.s.Edit("x", func(p *model.Project) error {
		c, _, _ := p.Clip(inter.ID)
		c.Opacity = 0.4
		return nil
	})
	run(t, f, "clip.resetOpacity")
	if clipNamed(t, f.s.Project, "Interview_Anna.mov").Opacity != 1 {
		t.Fatal("opacity not reset")
	}
	run(t, f, "clip.showInPool")
	if f.nav != page.Media || f.s.SelectedMedia != inter.MediaID {
		t.Fatalf("show in pool: nav %q media %q", f.nav, f.s.SelectedMedia)
	}
}

func TestGeneratorsUseSettings(t *testing.T) {
	f := newFixture(t, basicProject())
	_ = settings.Update(func(s *settings.Settings) { s.Timeline.GeneratorSecs = 3 })
	f.s.Seek(10)
	run(t, f, "clip.addTitle")
	c, ok := f.s.Clip()
	if !ok || c.Kind != model.KindTitle || c.Length != 75 {
		t.Fatalf("title should be 3 s long: %+v", c)
	}
	run(t, f, "clip.addLowerThird")
	if c, _ := f.s.Clip(); c.Name != "Bauchbinde" {
		t.Fatal("lower third")
	}
	run(t, f, "clip.addColor")
	if c, _ := f.s.Clip(); c.Kind != model.KindColor {
		t.Fatal("colour matte")
	}
}

func TestRenameClipDialog(t *testing.T) {
	f := newFixture(t, basicProject())
	f.s.SelectClip(clipNamed(t, f.s.Project, "Titel").ID)
	run(t, f, "clip.rename")
	e := overlay[*widget.Entry](t, f.w)
	if len(e) == 0 || e[0].Text != "Titel" {
		t.Fatal("rename dialog should show the name")
	}
	e[0].SetText("Vorspann")
	e[0].TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if clipNamed(t, f.s.Project, "Vorspann").Kind != model.KindTitle || f.s.UndoLabel() != "Clip umbenennen" {
		t.Fatal("rename not applied")
	}
	if f.w.Canvas().Overlays().Top() != nil {
		t.Fatal("dialog should close")
	}
}

func TestTrackCommands(t *testing.T) {
	f := newFixture(t, basicProject())
	run(t, f, "timeline.addAudioTrack")
	run(t, f, "timeline.addVideoTrack")
	if len(f.s.Project.Tracks) != 5 {
		t.Fatal("tracks not added")
	}
	run(t, f, "timeline.renameTrack")
	if _, ok := f.w.Canvas().Focused().(*widget.Entry); !ok {
		if f.w.Canvas().Focused() == nil {
			t.Fatal("rename should focus the inline entry")
		}
	}
	f.pg.tl.Escape()
	if !enabled(f, "timeline.removeEmptyTracks") {
		t.Fatal("empty tracks exist")
	}
	run(t, f, "timeline.removeEmptyTracks")
	p := f.s.Project
	if len(p.TrackIndices(model.TrackVideo)) != 2 || len(p.TrackIndices(model.TrackAudio)) != 1 {
		t.Fatalf("one audio track and both used video tracks should stay: %d", len(p.Tracks))
	}
	f.s.SelectTrack(p.Tracks[2].ID)
	run(t, f, "timeline.deleteTrack")
	if len(f.s.Project.Tracks) != 2 {
		t.Fatal("empty track should go without asking")
	}
}

func TestRangeAndZoomCommands(t *testing.T) {
	f := newFixture(t, basicProject())
	f.s.SelectClip(clipNamed(t, f.s.Project, "Titel").ID)
	run(t, f, "timeline.rangeFromSelection")
	if f.s.In != 25 || f.s.Out != 75 {
		t.Fatalf("range %d-%d", f.s.In, f.s.Out)
	}
	run(t, f, "timeline.clearRange")
	if f.s.In != 0 || f.s.Out != 0 || enabled(f, "timeline.clearRange") {
		t.Fatal("range not cleared")
	}
	f.s.Seek(40)
	run(t, f, "timeline.setIn")
	f.s.Seek(60)
	run(t, f, "timeline.setOut")
	if f.s.In != 40 || f.s.Out != 60 {
		t.Fatal("in/out")
	}
	if enabled(f, "timeline.zoomOut") || enabled(f, "timeline.zoomFit") {
		t.Fatal("zoom out and fit need a zoomed view")
	}
	run(t, f, "timeline.zoomIn")
	run(t, f, "timeline.zoomIn")
	if f.pg.tl.Zoom() != 2.25 {
		t.Fatalf("zoom %v", f.pg.tl.Zoom())
	}
	run(t, f, "timeline.zoomFit")
	if f.pg.tl.Zoom() != 1 || f.pg.bar.zoom.Value != 1 {
		t.Fatal("fit should reset the zoom")
	}
}

func TestSelectionCommands(t *testing.T) {
	f := newFixture(t, demoProject())
	f.s.Seek(250)
	run(t, f, "edit.selectForward")
	for _, id := range f.s.Selected {
		if c, _, _ := f.s.Project.Clip(id); c.Start < 250 {
			t.Fatalf("clip at %d before the playhead selected", c.Start)
		}
	}
	if len(f.s.Selected) < 3 {
		t.Fatalf("forward selection too small: %d", len(f.s.Selected))
	}
	inter := clipNamed(t, f.s.Project, "Interview_Anna.mov")
	f.s.SelectClip(inter.ID)
	run(t, f, "edit.selectLinked")
	if len(f.s.Selected) != 2 || f.s.SelectedClip != inter.ID {
		t.Fatalf("linked sound not added or primary changed: %v", f.s.Selected)
	}
	f.s.SelectClip(inter.ID)
	run(t, f, "edit.selectTrack")
	if len(f.s.Selected) != 3 {
		t.Fatalf("V1 has three clips, selected %d", len(f.s.Selected))
	}
	run(t, f, "edit.deselect")
	if len(f.s.Selected) != 0 || enabled(f, "edit.deselect") {
		t.Fatal("Esc should clear the selection")
	}
	run(t, f, "edit.selectAll")
	if len(f.s.Selected) != 8 {
		t.Fatalf("select all: %d", len(f.s.Selected))
	}
}

func TestRippleDeleteFollowsSyncLock(t *testing.T) {
	build := func() *model.Project {
		p := basicProject() // V1 colour 0-100, V2 title 25-75
		_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Nach", 100, 50))
		_ = p.PlaceClip(p.Tracks[1].ID, model.NewClip(model.KindTitle, "Spät", 200, 20))
		return p
	}
	f := newFixture(t, build())
	_ = settings.Update(func(s *settings.Settings) { s.Timeline.RippleSyncLock = false })
	if f.pg.tl.RippleSync() {
		t.Fatal("timeline should follow the sync lock setting")
	}
	f.s.SelectClip(clipNamed(t, f.s.Project, "Farbe").ID)
	run(t, f, "edit.rippleDelete")
	if clipNamed(t, f.s.Project, "Nach").Start != 0 || clipNamed(t, f.s.Project, "Spät").Start != 200 {
		t.Fatal("without sync lock only the edited track shifts")
	}
	f.s.Undo()
	_ = settings.Update(func(s *settings.Settings) { s.Timeline.RippleSyncLock = true })
	f.s.SelectClip(clipNamed(t, f.s.Project, "Farbe").ID)
	f.pg.run("edit.rippleDelete")
	if clipNamed(t, f.s.Project, "Nach").Start != 0 || clipNamed(t, f.s.Project, "Spät").Start != 100 {
		t.Fatal("with sync lock every track shifts")
	}
	f.s.Undo()
	// a clip spanning the gap blocks a synced ripple and is reported
	f.s.Edit("x", func(p *model.Project) error {
		return p.PlaceClip(p.Tracks[1].ID, model.NewClip(model.KindTitle, "Quer", 90, 30))
	})
	f.s.SelectClip(clipNamed(t, f.s.Project, "Farbe").ID)
	f.pg.run("edit.rippleDelete")
	var blocked *model.RippleBlockedError
	if len(f.errs) == 0 || !errors.As(f.errs[len(f.errs)-1], &blocked) {
		t.Fatalf("synced ripple should be blocked by V2: %v", f.errs)
	}
}

func TestMarkerCommands(t *testing.T) {
	f := newFixture(t, basicProject())
	if enabled(f, "marker.edit") || enabled(f, "marker.deleteAll") || enabled(f, "marker.next") {
		t.Fatal("marker commands need markers")
	}
	f.s.Seek(10)
	run(t, f, "marker.add")
	f.s.Seek(60)
	run(t, f, "marker.add")
	f.s.Seek(0)
	run(t, f, "marker.next")
	if f.s.Playhead != 10 {
		t.Fatal("next marker")
	}
	run(t, f, "marker.next")
	run(t, f, "marker.prev")
	if f.s.Playhead != 10 {
		t.Fatal("prev marker")
	}
	run(t, f, "marker.edit")
	entries := overlay[*widget.Entry](t, f.w)
	chips := overlay[*kit.Swatch](t, f.w)
	if len(entries) != 1 || entries[0].Text != "Marker 1" || len(chips) != 8 {
		t.Fatalf("edit dialog: %d entries, %d colours", len(entries), len(chips))
	}
	entries[0].SetText("Einsatz Musik")
	test.Tap(chips[4])
	tapOverlay(t, f.w, "Übernehmen")
	m := f.s.Project.Markers[0]
	if m.Name != "Einsatz Musik" || m.Color != markerColors()[4] || f.s.UndoLabel() != "Marker bearbeiten" {
		t.Fatalf("marker not edited: %+v", m)
	}
	run(t, f, "marker.delete")
	if len(f.s.Project.Markers) != 1 {
		t.Fatal("marker at playhead not deleted")
	}
	run(t, f, "marker.deleteAll")
	if len(f.s.Project.Markers) != 1 {
		t.Fatal("delete all must ask first")
	}
	tapOverlay(t, f.w, "Löschen")
	if len(f.s.Project.Markers) != 0 {
		t.Fatal("markers not deleted after confirming")
	}
}

func TestViewerFullscreen(t *testing.T) {
	f := newFixture(t, basicProject())
	f.pg.Activate()
	run(t, f, "view.viewerFullscreen")
	if !checked(f, "view.viewerFullscreen") || f.w.Canvas().Overlays().Top() == nil {
		t.Fatal("viewer should fill the window")
	}
	host := f.w.Canvas().Focused()
	if host == nil {
		t.Fatal("full-window viewer should take the keys")
	}
	host.TypedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if !f.s.Playing {
		t.Fatal("space should play in the full-window viewer")
	}
	f.s.SetPlaying(false)
	host.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if checked(f, "view.viewerFullscreen") || f.w.Canvas().Overlays().Top() != nil || len(f.pg.progBody.Objects) != 1 {
		t.Fatal("Esc should return the viewer to its panel")
	}
	if !f.pg.viewer.Active() {
		t.Fatal("viewer should render after returning")
	}
}

func TestSettingsReachTimelineAndViewer(t *testing.T) {
	f := newFixture(t, basicProject())
	f.pg.Activate()
	_ = settings.Update(func(s *settings.Settings) {
		s.Timeline.VideoTrackH, s.Timeline.AudioTrackH = 90, 40
		s.Timeline.ShowFilmstrip, s.Timeline.ShowWaveforms = false, false
		s.Timeline.StillSeconds = 2
		s.Playback.Follow = "off"
		s.Playback.SafeArea = true
	})
	if v, a := f.pg.tl.TrackHeights(); v != 90 || a != 40 {
		t.Fatalf("track heights %v/%v", v, a)
	}
	if f.pg.tl.ShowFilmstrip() || f.pg.tl.ShowWaveforms() || checked(f, "timeline.showFilmstrip") {
		t.Fatal("filmstrip and waveform settings not applied")
	}
	if f.pg.tl.Follow() != timeline.FollowOff || !checked(f, "timeline.follow.off") {
		t.Fatal("follow mode")
	}
	if !f.pg.viewer.SafeAreaShown() || !checked(f, "view.safeArea") {
		t.Fatal("safe area guides should show")
	}
	run(t, f, "timeline.follow.smooth")
	if settings.Get().Playback.Follow != "smooth" || f.pg.tl.Follow() != timeline.FollowSmooth {
		t.Fatal("follow command should change the setting")
	}
	run(t, f, "timeline.showWaveforms")
	if !f.pg.tl.ShowWaveforms() {
		t.Fatal("waveform toggle")
	}
	// stills follow StillSeconds
	id := f.s.Project.AddMedia(model.MediaItem{Name: "Foto.jpg", Kind: model.KindImage, Width: 100, Height: 100})
	f.s.Seek(200)
	f.pg.insertMedia(id, false)
	if c, _ := f.s.Clip(); c.Length != 50 {
		t.Fatalf("still should be 2 s: %d", c.Length)
	}
	// preview quality reaches the viewer
	w, h := f.pg.viewer.RenderSize()
	if w == 0 {
		t.Fatal("viewer not laid out")
	}
	_ = settings.Update(func(s *settings.Settings) { s.Playback.PreviewQuality = "quarter" })
	if q, qh := f.pg.viewer.RenderSize(); f.pg.viewer.Quality() != viewer.QualityQuarter || qh*4 > h+4 || q*4 > w+4 {
		t.Fatalf("quarter quality renders %dx%d of %dx%d", q, qh, w, h)
	}
}

func TestLayoutRoundTripWithHiddenViewer(t *testing.T) {
	f := newFixture(t, demoProject())
	f.pg.Activate()
	run(t, f, CmdHideViewer)
	if f.pg.vsplit != nil || f.pg.viewer.Active() {
		t.Fatal("hidden viewer should leave the monitor row and stop rendering")
	}
	if w, full := f.pg.tl.Size().Width, f.w.Canvas().Size().Width; w < full*0.4 {
		t.Fatalf("timeline should take the centre column: %v of %v", w, full)
	}
	if h, full := f.pg.tl.Size().Height, f.w.Canvas().Size().Height; h < full*0.8 {
		t.Fatalf("timeline should take the full height: %v of %v", h, full)
	}
	run(t, f, CmdHideLibrary)
	f.pg.rsplit.SetOffset(0.8) // the user drags the inspector divider
	st := f.pg.LayoutState()
	if !st.Hidden["viewer"] || !st.Hidden["library"] || st.Hidden["inspector"] || st.Splits["inspector"] != 0.8 {
		t.Fatalf("state %+v", st)
	}
	if _, ok := st.Splits["timeline"]; ok {
		t.Fatal("untouched splits should keep following the window")
	}

	// a second page restores it
	g := newFixture(t, demoProject())
	g.pg.ApplyLayout(st)
	if g.pg.vsplit != nil || g.pg.hsplit != nil || g.pg.rsplit == nil || g.pg.rsplit.Offset != 0.8 {
		t.Fatal("layout not restored")
	}
	if checked(g, CmdHideViewer) || checked(g, CmdHideLibrary) || !checked(g, CmdHideInspector) {
		t.Fatal("only the shown inspector should be checked")
	}
	g.w.Resize(fyne.NewSize(1300, 820))
	if g.pg.rsplit.Offset != 0.8 {
		t.Fatal("restored divider should stay")
	}
	g.pg.ApplyLayout(layout.Page{})
	if g.pg.vsplit == nil || g.pg.hsplit == nil || g.pg.rsplit == nil {
		t.Fatal("an empty layout shows every area")
	}
	// before Content the layout waits
	h := newPage(&page.Context{Session: session.New(basicProject())}, "")
	h.ApplyLayout(st)
	test.NewTempWindow(t, h.Content())
	defer h.Close()
	if h.vsplit != nil || h.hsplit != nil {
		t.Fatal("pending layout not applied")
	}
}

func TestPanelsBuildAndClose(t *testing.T) {
	app := test.NewTempApp(t)
	app.Settings().SetTheme(studiotheme.New())
	kit.UseUIQueue(t)
	resetSettings(t)
	s := session.New(demoProject())
	cache := media.NewCache()
	before := runtime.NumGoroutine()
	for _, id := range []string{page.PanelProgram, page.PanelCleanFeed, page.PanelTimeline, page.PanelInspector, page.PanelLibrary} {
		info, ok := page.PanelByID(id)
		if !ok || info.Title == "" || info.New == nil {
			t.Fatalf("panel %s not registered", id)
		}
		ctx := &page.Context{Session: s, Renderer: render.New(cache), Media: cache}
		w := test.NewTempWindow(t, nil)
		ctx.Window = w
		pn := info.New(ctx)
		w.SetContent(pn.Content())
		w.Resize(fyne.NewSize(900, 600))
		s.Seek(30)
		pn.Close()
		pn.Close() // twice is harmless
	}
	sharedMu.Lock()
	left := len(sharedBy)
	sharedMu.Unlock()
	if left != 0 {
		t.Fatal("shared state not released")
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before+1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+1 {
		t.Fatalf("goroutines leaked: %d before, %d after", before, n)
	}
}

func TestTimelinePanelFollowsPage(t *testing.T) {
	f := newFixture(t, basicProject())
	pn := NewPanel(&page.Context{Session: f.s, Window: f.w}, page.PanelTimeline)
	defer pn.Close()
	run(t, f, "tool.ripple")
	if pn.pg.tl.Tool() != timeline.ToolRipple || !pn.pg.bar.tools[2].on {
		t.Fatal("the panel timeline should use the page's tool")
	}
	// keys in the panel window run edit commands on the shared session
	f.s.Seek(50)
	if !pn.Shortcut(fyne.KeyK, fyne.KeyModifierControl) || len(f.s.Project.Tracks[0].Clips) != 2 {
		t.Fatal("S in the panel should split")
	}
	// zoom is per timeline
	pn.TypedKey(&fyne.KeyEvent{Name: fyne.KeyPlus})
	if pn.pg.zoom == 1 || f.pg.zoom != 1 {
		t.Fatal("panel zoom should be its own")
	}
	// clean feed: no transport, no guides
	cf := NewPanel(&page.Context{Session: f.s}, page.PanelCleanFeed)
	defer cf.Close()
	_ = settings.Update(func(s *settings.Settings) { s.Playback.SafeArea = true })
	if cf.pg.viewer.SafeAreaShown() || cf.pg.transport != nil {
		t.Fatal("clean feed must stay clean")
	}
}

// overlay finds widgets of type T in the window's overlays (dialogs).
func overlay[T any](t *testing.T, w fyne.Window) []T {
	t.Helper()
	var out []T
	for _, o := range w.Canvas().Overlays().List() {
		out = append(out, find[T](o)...)
	}
	return out
}

func tapOverlay(t *testing.T, w fyne.Window, label string) {
	t.Helper()
	for _, b := range overlay[*widget.Button](t, w) {
		if b.Text == label {
			test.Tap(b)
			return
		}
	}
	t.Fatalf("no button %q", label)
}
