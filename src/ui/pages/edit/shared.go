package edit

import (
	"fmt"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/page"
	"framestudio-proto/src/ui/timeline"
)

// shared is the editing state every timeline of one project follows: the edit page's
// and those of detached timeline panels use the same tool.
type shared struct {
	tool     timeline.Tool
	bladeAll bool
	pages    []*Page
}

var (
	sharedMu sync.Mutex // guards sharedBy; the state itself is UI-thread only
	sharedBy = map[*session.Session]*shared{}
)

func acquireShared(p *Page) *shared {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	st := sharedBy[p.ctx.Session]
	if st == nil {
		st = &shared{}
		sharedBy[p.ctx.Session] = st
	}
	st.pages = append(st.pages, p)
	return st
}

func releaseShared(p *Page) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	st := sharedBy[p.ctx.Session]
	if st == nil {
		return
	}
	for i, x := range st.pages {
		if x == p {
			st.pages = append(st.pages[:i], st.pages[i+1:]...)
			break
		}
	}
	if len(st.pages) == 0 {
		delete(sharedBy, p.ctx.Session)
	}
}

func (st *shared) each(fn func(p *Page)) {
	sharedMu.Lock()
	pages := append([]*Page(nil), st.pages...)
	sharedMu.Unlock()
	for _, p := range pages {
		fn(p)
	}
}

func (st *shared) setTool(t timeline.Tool) {
	st.tool = t
	st.each(func(p *Page) {
		p.tool = t
		if p.tl != nil {
			p.tl.SetTool(t)
		}
		if p.bar != nil {
			p.bar.update()
		}
	})
}

func (st *shared) setBladeAll(on bool) {
	st.bladeAll = on
	st.each(func(p *Page) {
		p.bladeAll = on
		if p.tl != nil {
			p.tl.SetBladeAllTracks(on)
		}
		if p.bar != nil {
			p.bar.update()
		}
	})
}

// applyPrefs follows a settings change (UI thread, settings.Subscribe).
func (p *Page) applyPrefs(s settings.Settings) {
	if p.closed {
		return
	}
	old := p.prefs
	p.prefs = s
	p.applyTimelinePrefs()
	if p.viewer != nil && p.panel != page.PanelCleanFeed {
		p.viewer.ShowSafeArea(s.Playback.SafeArea)
	}
	if p.bar != nil {
		p.bar.update()
	}
	if p.root != nil && (old.Timeline.VideoTrackH != s.Timeline.VideoTrackH || old.Timeline.AudioTrackH != s.Timeline.AudioTrackH) {
		p.root.Refresh() // the timeline panel follows the track heights
	}
}

// applyTimelinePrefs hands the timeline settings to the timeline widget.
func (p *Page) applyTimelinePrefs() {
	t := p.prefs.Timeline
	p.snapping = t.Snapping
	if p.tl == nil {
		return
	}
	p.tl.SetSnapping(t.Snapping)
	p.tl.SetTrackHeights(float32(t.VideoTrackH), float32(t.AudioTrackH))
	p.tl.SetShowFilmstrip(t.ShowFilmstrip)
	p.tl.SetShowWaveforms(t.ShowWaveforms)
	p.tl.SetRippleSync(t.RippleSyncLock)
	p.tl.SetStillSeconds(t.StillSeconds)
	p.tl.SetFollow(p.prefs.Playback.Follow)
}

// updateSettings changes and saves the settings; every page and panel follows.
func (p *Page) updateSettings(fn func(s *settings.Settings)) {
	if err := settings.Update(fn); err != nil {
		p.showErr(fmt.Errorf("Die Einstellung gilt, konnte aber nicht gespeichert werden: %w", err))
	}
}

// heldModifiers returns the modifier keys of the current key event (desktop driver).
func heldModifiers() fyne.KeyModifier {
	if a := fyne.CurrentApp(); a != nil {
		if d, ok := a.Driver().(desktop.Driver); ok {
			return d.CurrentKeyModifiers()
		}
	}
	return 0
}
