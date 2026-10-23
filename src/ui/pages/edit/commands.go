package edit

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"framestudio-proto/src/model"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/timeline"
)

// Commands outside the catalog: show or hide areas of the edit page (category Ansicht).
const (
	CmdHideViewer    = "edit.hide.viewer"
	CmdHideLibrary   = "edit.hide.library"
	CmdHideInspector = "edit.hide.inspector"
)

// Commands returns the edit page's command bindings (page.CommandProvider). The shell
// binds them in command.PageScope(page.Edit).
func (p *Page) Commands() []command.Binding { return p.bindings() }

// bindings lists every command the page implements. Run and Enabled work on the live
// session; parts a panel does not have (viewer, timeline) disable their commands.
func (p *Page) bindings() []command.Binding {
	s := p.ctx.Session
	hasSel := func() bool { return len(p.selection()) > 0 }
	single := func() bool { _, ok := s.Clip(); return ok }
	hasTL := func() bool { return p.tl != nil }
	onPage := func() bool { return p.panel == "" && p.frame != nil }
	fade := fmtSeconds(p.prefs.Timeline.FadeSeconds)

	bs := []command.Binding{
		{ID: "edit.cut", Run: p.cut, Enabled: hasSel},
		{ID: "edit.copy", Run: p.copySelection, Enabled: hasSel},
		{ID: "edit.paste", Run: p.paste, Enabled: clipboardFull},
		{ID: "edit.duplicate", Run: p.duplicate, Enabled: hasSel},
		{ID: "edit.delete", Run: func() { p.deleteSelected(false) }, Enabled: hasSel},
		{ID: "edit.rippleDelete", Run: func() { p.deleteSelected(true) }, Enabled: hasSel},
		{ID: "edit.selectAll", Run: s.SelectAll, Enabled: func() bool { return len(p.selectableClips(func(model.Clip) bool { return true })) > 0 }},
		{ID: "edit.deselect", Run: p.escape, Enabled: p.canEscape},
		{ID: "edit.selectForward", Run: p.selectForward, Enabled: func() bool { return len(p.forwardClips()) > 0 }},
		{ID: "edit.selectTrack", Run: p.selectTrack, Enabled: func() bool { return len(p.trackClips()) > 0 }},
		{ID: "edit.selectLinked", Run: p.selectLinked, Enabled: func() bool { return len(p.linkedMissing()) > 0 }},

		// enabled whenever there are clips, so S explains why nothing can be cut
		{ID: "clip.split", Run: p.split, Enabled: func() bool { return s.Project.Duration() > 0 }},
		{ID: "clip.splitAll", Run: p.splitAll, Enabled: p.canSplitAll},
		{ID: "clip.rename", Run: p.renameClip, Enabled: func() bool { return single() && p.ctx.Window != nil }},
		{ID: "clip.fadeIn", Run: func() { p.setFades(true, false) }, Enabled: hasSel,
			Info: &command.Info{Label: "Einblenden (" + fade + ")"}},
		{ID: "clip.fadeOut", Run: func() { p.setFades(false, true) }, Enabled: hasSel,
			Info: &command.Info{Label: "Ausblenden (" + fade + ")"}},
		{ID: "clip.removeFades", Run: p.removeFades, Enabled: func() bool { return p.anySelected(hasFade) }},
		{ID: "clip.resetOpacity", Run: p.resetOpacity, Enabled: func() bool {
			return p.anySelected(func(c *model.Clip) bool { return c.Kind.IsVisual() && c.Opacity != 1 })
		}},
		{ID: "clip.showInPool", Run: p.showInPool, Enabled: func() bool { return p.primaryMedia() != "" }},
		{ID: "clip.addTitle", Run: func() { p.insertGenerator("title") }},
		{ID: "clip.addLowerThird", Run: func() { p.insertGenerator("lower") }},
		{ID: "clip.addColor", Run: func() { p.insertGenerator("color") }},

		{ID: "timeline.snapping", Run: p.toggleSnap, Checked: func() bool { return p.snapping }},
		{ID: "timeline.bladeAll", Run: func() { p.setBladeAll(!p.bladeAll) }, Checked: func() bool { return p.bladeAll }},
		{ID: "timeline.addVideoTrack", Run: func() { p.addTrack(model.TrackVideo) }},
		{ID: "timeline.addAudioTrack", Run: func() { p.addTrack(model.TrackAudio) }},
		{ID: "timeline.deleteTrack", Run: p.removeSelectedTrack, Enabled: func() bool {
			t, _ := s.Project.Track(s.SelectedTrack)
			return t != nil && !t.Locked
		}},
		{ID: "timeline.renameTrack", Run: func() { p.tl.StartRename(s.SelectedTrack) }, Enabled: func() bool {
			t, _ := s.Project.Track(s.SelectedTrack)
			return t != nil && p.tl != nil
		}},
		{ID: "timeline.removeEmptyTracks", Run: p.removeEmptyTracks, Enabled: func() bool { return len(p.emptyTracks()) > 0 }},
		{ID: "timeline.setIn", Run: p.markIn},
		{ID: "timeline.setOut", Run: p.markOut},
		{ID: "timeline.clearRange", Run: s.ClearRange, Enabled: func() bool { return s.In > 0 || s.Out > 0 }},
		{ID: "timeline.rangeFromSelection", Run: p.rangeFromSelection, Enabled: hasSel},
		{ID: "timeline.zoomIn", Run: func() { p.zoomBy(1.5) }, Enabled: func() bool { return hasTL() && p.zoom < maxZoom }},
		{ID: "timeline.zoomOut", Run: func() { p.zoomBy(1 / 1.5) }, Enabled: func() bool { return hasTL() && p.zoom > minZoom }},
		{ID: "timeline.zoomFit", Run: func() { p.setZoom(minZoom) }, Enabled: func() bool { return hasTL() && p.zoom != minZoom }},
		{ID: "timeline.showFilmstrip", Checked: func() bool { return p.prefs.Timeline.ShowFilmstrip },
			Run: func() {
				p.updateSettings(func(s *settings.Settings) { s.Timeline.ShowFilmstrip = !s.Timeline.ShowFilmstrip })
			}},
		{ID: "timeline.showWaveforms", Checked: func() bool { return p.prefs.Timeline.ShowWaveforms },
			Run: func() {
				p.updateSettings(func(s *settings.Settings) { s.Timeline.ShowWaveforms = !s.Timeline.ShowWaveforms })
			}},

		{ID: "marker.add", Run: p.addMarker},
		{ID: "marker.edit", Run: p.editMarker, Enabled: func() bool { return p.markerAtPlayhead() >= 0 && p.ctx.Window != nil }},
		{ID: "marker.delete", Run: p.deleteMarker, Enabled: func() bool { return p.markerAtPlayhead() >= 0 }},
		{ID: "marker.deleteAll", Run: p.deleteAllMarkers, Enabled: func() bool { return len(s.Project.Markers) > 0 }},
		{ID: "marker.next", Run: func() { p.seekMarker(+1) }, Enabled: func() bool { return p.markerFrom(+1) >= 0 }},
		{ID: "marker.prev", Run: func() { p.seekMarker(-1) }, Enabled: func() bool { return p.markerFrom(-1) >= 0 }},

		{ID: "view.viewerFullscreen", Run: p.toggleFullscreen, Enabled: p.canFullscreen, Checked: func() bool { return p.fs != nil }},
		{ID: "view.safeArea", Checked: func() bool { return p.prefs.Playback.SafeArea },
			Run: func() { p.updateSettings(func(s *settings.Settings) { s.Playback.SafeArea = !s.Playback.SafeArea }) }},
		p.hideBinding(CmdHideViewer, areaViewer, "Programm-Monitor ausblenden", onPage),
		p.hideBinding(CmdHideLibrary, areaLibrary, "Bibliothek ausblenden", onPage),
		p.hideBinding(CmdHideInspector, areaInspector, "Inspector ausblenden", onPage),
	}
	for _, d := range toolDefs {
		t := d.tool
		bs = append(bs, command.Binding{ID: d.id, Run: func() { p.setTool(t) }, Checked: func() bool { return p.tool == t }})
	}
	for i, f := range []model.Fit{model.FitContain, model.FitCover, model.FitStretch} {
		id := []string{"clip.fit.contain", "clip.fit.cover", "clip.fit.stretch"}[i]
		bs = append(bs, command.Binding{ID: id, Run: func() { p.setFit(f) },
			Enabled: func() bool { return p.anySelected(hasFit) },
			Checked: func() bool { return p.allFit(f) }})
	}
	for i, cc := range studiotheme.ClipColors {
		col := model.FromNRGBA(cc.Color)
		bs = append(bs, command.Binding{ID: "clip.color." + strconv.Itoa(i), Run: func() { p.setLabelColor(col) },
			Enabled: func() bool { return p.anySelected(labelled) },
			Checked: func() bool { c, ok := s.Clip(); return ok && c.Color == col }})
	}
	for _, f := range []struct{ id, mode string }{
		{"timeline.follow.page", timeline.FollowPage}, {"timeline.follow.smooth", timeline.FollowSmooth}, {"timeline.follow.off", timeline.FollowOff},
	} {
		mode := f.mode
		bs = append(bs, command.Binding{ID: f.id,
			Run:     func() { p.updateSettings(func(s *settings.Settings) { s.Playback.Follow = mode }) },
			Checked: func() bool { return followMode(p.prefs.Playback.Follow) == mode }})
	}
	return bs
}

func (p *Page) hideBinding(id, area, label string, enabled func() bool) command.Binding {
	return command.Binding{ID: id, Info: &command.Info{Label: label, Category: "Ansicht"},
		Run:     func() { p.setHidden(area, !p.hidden[area]) },
		Enabled: enabled,
		Checked: func() bool { return p.hidden[area] }}
}

func followMode(m string) string {
	switch m {
	case timeline.FollowSmooth, timeline.FollowOff:
		return m
	}
	return timeline.FollowPage
}

// fmtSeconds formats a duration from the settings for labels: "1 s", "0,5 s".
func fmtSeconds(sec float64) string {
	if sec <= 0 {
		sec = 1
	}
	return strings.Replace(strconv.FormatFloat(sec, 'f', -1, 64), ".", ",", 1) + " s"
}

// run executes command id for a button or menu of this page: through the registry when
// it holds an active binding (so menus and the palette see the same state), otherwise
// with the page's own binding (detached panels, a page without shell).
func (p *Page) run(id string) {
	if p.panel == "" {
		if c, ok := p.reg.Get(id); ok && c.Available {
			p.reg.Run(id)
			return
		}
	}
	for _, b := range p.bindings() {
		if b.ID == id {
			if b.Run != nil && (b.Enabled == nil || b.Enabled()) {
				b.Run()
			}
			p.reg.Refresh()
			return
		}
	}
}

// dispatch runs the page command whose effective shortcut (user key map included) is
// key with mods. It reports whether one ran.
func (p *Page) dispatch(key fyne.KeyName, mods fyne.KeyModifier) bool {
	sc := command.Format(key, mods)
	if sc == "" {
		return false
	}
	for _, b := range p.bindings() {
		if command.Normalize(p.reg.Shortcut(b.ID)) != sc {
			continue
		}
		if b.Run == nil || (b.Enabled != nil && !b.Enabled()) {
			continue
		}
		b.Run()
		p.reg.Refresh()
		return true
	}
	return false
}

// commandsChanged follows registry changes (key map) in the tooltips.
func (p *Page) commandsChanged() {
	if p.bar != nil && !p.closed {
		p.bar.updateTips()
	}
}

// escape cancels what is going on: full-window viewer, media drag, timeline gesture or
// rename, else the clip selection.
func (p *Page) escape() {
	switch {
	case p.fs != nil:
		p.exitFullscreen()
	case kit.MediaDragActive():
		kit.CancelMediaDrag()
	case p.tl != nil && p.tl.Escape():
	default:
		p.ctx.Session.SelectClip("")
	}
}

func (p *Page) canEscape() bool {
	return p.fs != nil || kit.MediaDragActive() || (p.tl != nil && p.tl.CanEscape()) || len(p.selection()) > 0
}

// selectableClips returns the clips on unlocked tracks that match.
func (p *Page) selectableClips(match func(model.Clip) bool) []string {
	var ids []string
	for _, t := range p.ctx.Session.Project.Tracks {
		if t.Locked {
			continue
		}
		for _, c := range t.Clips {
			if match(c) {
				ids = append(ids, c.ID)
			}
		}
	}
	return ids
}

// forwardClips are the clips that start at or after the playhead.
func (p *Page) forwardClips() []string {
	ph := p.ctx.Session.Playhead
	return p.selectableClips(func(c model.Clip) bool { return c.Start >= ph })
}

func (p *Page) selectForward() { p.ctx.Session.SelectGroup(p.forwardClips()) }

// trackClips are the clips of the selected track (if it is unlocked).
func (p *Page) trackClips() []string {
	s := p.ctx.Session
	t, _ := s.Project.Track(s.SelectedTrack)
	if t == nil || t.Locked {
		return nil
	}
	var ids []string
	for _, c := range t.Clips {
		ids = append(ids, c.ID)
	}
	return ids
}

func (p *Page) selectTrack() { p.ctx.Session.SelectGroup(p.trackClips()) }

// linkedMissing returns the linked partners (picture and sound of one video) of the
// selected clips that are not selected yet.
func (p *Page) linkedMissing() []string {
	s := p.ctx.Session
	var out []string
	seen := map[string]bool{}
	for _, id := range p.selection() {
		for _, l := range s.Project.LinkedClips(id) {
			if !s.IsSelected(l) && l != s.SelectedClip && !seen[l] {
				if _, ti, _ := s.Project.Clip(l); !s.Project.Tracks[ti].Locked {
					seen[l] = true
					out = append(out, l)
				}
			}
		}
	}
	return out
}

func (p *Page) selectLinked() {
	s := p.ctx.Session
	// the primary clip stays primary (it is listed last)
	ids := append(p.linkedMissing(), p.selection()...)
	if s.SelectedClip != "" {
		ids = append(remove(ids, s.SelectedClip), s.SelectedClip)
	}
	s.SelectClips(ids)
}

func (p *Page) rangeFromSelection() {
	s := p.ctx.Session
	in, out := math.MaxInt, 0
	for _, id := range p.selection() {
		if c, _, _ := s.Project.Clip(id); c != nil {
			in, out = min(in, c.Start), max(out, c.End())
		}
	}
	if out <= in {
		return
	}
	s.In, s.Out = in, out
	s.SetOut(out) // notifies
}

// emptyTracks returns the IDs of unlocked tracks without clips that can go: at least
// one track of each kind stays.
func (p *Page) emptyTracks() []string {
	pr := p.ctx.Session.Project
	var out []string
	for _, k := range []model.TrackKind{model.TrackVideo, model.TrackAudio} {
		idx := pr.TrackIndices(k)
		var empty []string
		for _, i := range idx {
			if t := pr.Tracks[i]; len(t.Clips) == 0 && !t.Locked {
				empty = append(empty, t.ID)
			}
		}
		if len(empty) == len(idx) && len(empty) > 0 {
			empty = empty[1:] // keep the lowest one
		}
		out = append(out, empty...)
	}
	return out
}

func (p *Page) removeEmptyTracks() {
	ids := p.emptyTracks()
	if len(ids) == 0 {
		return
	}
	label := "Leere Spur entfernen"
	if len(ids) > 1 {
		label = fmt.Sprintf("%d leere Spuren entfernen", len(ids))
	}
	p.edit(label, func(pr *model.Project) error {
		for _, id := range ids {
			if err := pr.RemoveTrack(id); err != nil {
				return err
			}
		}
		return nil
	})
}

// anySelected reports whether a selected clip matches.
func (p *Page) anySelected(match func(*model.Clip) bool) bool {
	for _, id := range p.selection() {
		if c, _, _ := p.ctx.Session.Project.Clip(id); c != nil && match(c) {
			return true
		}
	}
	return false
}

// updateSelected changes every selected clip that match accepts in one undo step.
func (p *Page) updateSelected(label string, match func(*model.Clip) bool, fn func(c *model.Clip)) {
	ids := p.selection()
	if len(ids) == 0 {
		return
	}
	p.edit(label, func(pr *model.Project) error {
		n := 0
		for _, id := range ids {
			c, _, _ := pr.Clip(id)
			if c == nil || !match(c) {
				continue
			}
			next := *c
			fn(&next)
			if err := pr.UpdateClip(next); err != nil {
				return err
			}
			n++
		}
		if n == 0 {
			return errors.New("Die Auswahl enthält keinen passenden Clip")
		}
		return nil
	})
}

func hasFade(c *model.Clip) bool { return c.FadeIn > 0 || c.FadeOut > 0 }
func hasFit(c *model.Clip) bool  { return c.Kind == model.KindImage || c.Kind == model.KindVideo }

// setFades gives the selected clips a fade of the length set in the settings
// (Timeline.FadeSeconds), at most the clip length.
func (p *Page) setFades(in, out bool) {
	n := secondsToFrames(p.prefs.Timeline.FadeSeconds, p.ctx.Session.FPS())
	if p.prefs.Timeline.FadeSeconds <= 0 {
		n = p.ctx.Session.FPS()
	}
	label := "Einblenden"
	if out {
		label = "Ausblenden"
	}
	p.updateSelected(label, anyClip, func(c *model.Clip) {
		if in {
			c.FadeIn, c.FadeInFrom = min(n, c.Length), 0
		}
		if out {
			c.FadeOut, c.FadeOutTo = min(n, c.Length), 0
		}
	})
}

func (p *Page) removeFades() {
	p.updateSelected("Blenden entfernen", hasFade, func(c *model.Clip) {
		c.FadeIn, c.FadeOut, c.FadeInFrom, c.FadeOutTo = 0, 0, 0, 0
	})
}

func (p *Page) resetOpacity() {
	p.updateSelected("Deckkraft zurücksetzen", visualClip, func(c *model.Clip) { c.Opacity = 1 })
}

func (p *Page) setFit(f model.Fit) {
	p.updateSelected("Einpassung ändern", hasFit, func(c *model.Clip) { c.Fit = f })
}

// allFit reports whether every selected image or video clip uses f (and there is one).
func (p *Page) allFit(f model.Fit) bool {
	n := 0
	for _, id := range p.selection() {
		c, _, _ := p.ctx.Session.Project.Clip(id)
		if c == nil || !hasFit(c) {
			continue
		}
		cf := c.Fit
		if cf == "" {
			cf = model.FitContain
		}
		if cf != f {
			return false
		}
		n++
	}
	return n > 0
}

func (p *Page) setLabelColor(col model.RGBA) {
	p.updateSelected("Clipfarbe ändern", labelled, func(c *model.Clip) { c.Color = col })
}

// primaryMedia returns the media ID of the primary clip if the item is in the project.
func (p *Page) primaryMedia() string {
	s := p.ctx.Session
	c, ok := s.Clip()
	if !ok || c.MediaID == "" {
		return ""
	}
	if _, ok := s.Project.MediaByID(c.MediaID); !ok {
		return ""
	}
	return c.MediaID
}

// showInPool selects the primary clip's media and opens the media page.
func (p *Page) showInPool() {
	id := p.primaryMedia()
	if id == "" {
		return
	}
	p.ctx.Session.SelectMedia(id)
	if p.ctx.Navigate != nil {
		p.ctx.Navigate(page.Media)
	}
}

// clipMenuItems are the command entries the timeline adds to a clip's context menu.
func (p *Page) clipMenuItems(model.Clip) []*fyne.MenuItem {
	var items []*fyne.MenuItem
	for _, id := range []string{"edit.cut", "edit.copy", "edit.paste", "edit.duplicate", "",
		"clip.rename", "clip.fadeIn", "clip.fadeOut", "clip.removeFades", "", "clip.showInPool"} {
		if id == "" {
			items = append(items, fyne.NewMenuItemSeparator())
			continue
		}
		items = append(items, p.menuItem(id))
	}
	return items
}

// menuItem builds a menu entry for a page command with its label, shortcut and state.
func (p *Page) menuItem(id string) *fyne.MenuItem {
	var b *command.Binding
	for _, x := range p.bindings() {
		if x.ID == id {
			b = &x
			break
		}
	}
	label := id
	if c, ok := p.reg.Get(id); ok {
		label = c.Label
	}
	if b != nil && b.Info != nil && b.Info.Label != "" {
		label = b.Info.Label
	}
	it := fyne.NewMenuItem(label, func() { p.run(id) })
	if k, mods, ok := command.Parse(p.reg.Shortcut(id)); ok {
		it.Shortcut = &desktop.CustomShortcut{KeyName: k, Modifier: mods}
	}
	if b != nil {
		it.Disabled = b.Run == nil || (b.Enabled != nil && !b.Enabled())
		if b.Checked != nil {
			it.Checked = b.Checked()
		}
	}
	return it
}
