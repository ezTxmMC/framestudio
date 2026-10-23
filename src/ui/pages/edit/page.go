// Package edit is the "edit" workspace page.
//
// OWNER: edit page agent. New(ctx) is the contract; do not change its signature.
package edit

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
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

// Page is the edit workspace: library, program monitor, inspector and timeline. A
// detached panel (panels.go) is a Page that builds only its own part.
type Page struct {
	ctx  *page.Context
	post func(func()) // hands background results to the UI thread

	// reg is ctx.Commands, or a private registry when the context has none (tests,
	// tools); the page's bindings are then bound globally in it.
	reg    *command.Registry
	ownReg bool
	panel  string // "" for the page, else the page.Panel* ID this instance shows

	root         fyne.CanvasObject
	vsplit       *container.Split // monitor row over timeline (nil while the viewer is hidden)
	hsplit       *container.Split // library | rest (nil while the library is hidden)
	rsplit       *container.Split // centre | inspector (nil while the inspector is hidden)
	viewer       *viewer.Viewer
	statsStop    func()
	cacheStop    chan struct{}
	cacheBar     atomic.Pointer[[]timeline.CacheRange]
	transport    *viewer.TransportBar
	tl           *timeline.Timeline
	timelineHost *fyne.Container
	lib          *library
	insp         *inspector
	bar          *toolbar
	progName     *canvas.Text
	progMeta     *canvas.Text

	// areas of the page (arrange.go)
	areas    map[string]fyne.CanvasObject // area ID -> framed panel
	progBody *fyne.Container              // holds progBox inside the program panel
	progBox  fyne.CanvasObject            // viewer over transport
	hidden   map[string]bool
	offsets  map[string]float64 // split offsets while a split is not shown
	fit      *fitTimeline
	frame    *fyne.Container // root container; its object is the current arrangement
	fs       *fullscreen
	pending  *layout.Page // ApplyLayout before Content

	tool     timeline.Tool
	snapping bool
	bladeAll bool
	zoom     float32
	prefs    settings.Settings
	st       *shared
	active   bool

	attached fyne.Window // window the timeline was attached to for media drops
	unsub    func()
	unsubs   []func() // settings and registry subscriptions
	started  bool
	closed   bool

	// files dropped from the file manager (see filedrop.go)
	spawn    func(func()) // runs background work; tests replace it
	probe    probeFunc
	life     context.Context // ends on Close: stops probing dropped files
	stopLife context.CancelFunc

	// dlg is the dialog opened by the last command (rename, marker), for tests.
	dlg dialog.Dialog
}

var (
	_ page.CommandProvider = (*Page)(nil)
	_ page.LayoutAware     = (*Page)(nil)
	_ page.DropTarget      = (*Page)(nil)
)

func New(ctx *page.Context) page.Page { return newPage(ctx, "") }

func newPage(ctx *page.Context, panel string) *Page {
	p := &Page{ctx: ctx, post: kit.Do, snapping: true, zoom: 1, panel: panel,
		spawn: func(f func()) { go f() }, probe: media.ProbeContext,
		hidden: map[string]bool{}, offsets: map[string]float64{}, areas: map[string]fyne.CanvasObject{}}
	p.reg = ctx.Commands
	if p.reg == nil {
		p.reg, p.ownReg = command.NewRegistry(), true
	}
	p.prefs = settings.Get()
	return p
}

// windowAttacher is implemented by timelines that accept media drags from the library
// (timeline.AttachWindow). Asserted so the page builds with and without it.
type windowAttacher interface{ AttachWindow(fyne.Window) }

func (p *Page) Content() fyne.CanvasObject {
	if p.root != nil {
		return p.root
	}
	p.start()
	p.buildViewer(false)
	p.buildTimeline()
	p.wireCacheBar()
	p.buildLibrary()
	p.buildInspector()

	progInfo := container.NewHBox(container.NewCenter(p.progName), container.NewCenter(p.progMeta), fixed(4, 0))
	p.progBody = container.NewStack(p.progBox)
	p.areas[areaViewer] = kit.Panel("Programm", progInfo, p.progBody)
	p.areas[areaInspector] = kit.Panel("Inspector", container.NewHBox(p.insp.kind, fixed(6, 0)), p.insp.content)
	p.areas[areaLibrary] = kit.Panel("Bibliothek", nil, p.lib.content)
	p.areas[areaTimeline] = kit.Panel("Timeline", p.bar.content, p.timelineHost)
	p.offsets[splitLibrary], p.offsets[splitInspector], p.offsets[splitTimeline] = 0.2, 0.7, 0.6
	p.fit = &fitTimeline{pg: p}
	p.frame = container.New(p.fit)
	p.root = p.frame
	p.arrange()
	if l := p.pending; l != nil {
		p.pending = nil
		p.ApplyLayout(*l)
	}

	p.unsub = p.ctx.Session.Subscribe(p.changed)
	p.changed(session.ChangeProject | session.ChangeSelection | session.ChangeMeta)
	p.attachWindow()
	if p.ownReg {
		p.reg.Bind(command.Global, p.Commands()...)
	}
	return p.root
}

// start subscribes to the shared tool state, the settings and the command registry.
func (p *Page) start() {
	if p.started || p.closed {
		return
	}
	p.started = true
	p.st = acquireShared(p)
	p.tool, p.bladeAll = p.st.tool, p.st.bladeAll
	p.unsubs = append(p.unsubs,
		settings.Subscribe(func(s settings.Settings) { p.applyPrefs(s) }),
		p.reg.Subscribe(p.commandsChanged))
}

// buildViewer creates the program monitor with its transport bar and master meter, or
// a bare picture for the clean feed (no transport, no overlays).
func (p *Page) buildViewer(clean bool) {
	s := p.ctx.Session
	p.viewer = viewer.New(s, p.ctx.Renderer)
	p.viewer.SetDeliver(p.post)
	if clean {
		p.progBox = p.viewer
		return
	}
	p.transport = viewer.NewTransportBar(s, true)
	// the statistics overlay and the "Wiedergabe überlastet" chip sit over the picture
	p.viewer.SetStats(settings.Get().Playback.Stats)
	p.statsStop = settings.Subscribe(func(s settings.Settings) { p.viewer.SetStats(s.Playback.Stats) })
	p.progBox = container.NewBorder(nil, p.transport.Content(), nil, nil, p.viewer)
	p.progName = text("", studiotheme.Text, 12)
	p.progMeta = kit.MonoText("", studiotheme.Faint, 11)
	p.viewer.ShowSafeArea(p.prefs.Playback.SafeArea)
}

func (p *Page) buildTimeline() {
	p.tl = timeline.New(p.ctx.Session, p.ctx.Media)
	p.tl.OnError = func(err error) { p.showErr(explain(err)) }
	p.tl.OnZoomChanged = p.zoomChanged
	p.tl.SetTool(p.tool)
	p.tl.SetBladeAllTracks(p.bladeAll)
	p.tl.SetZoom(p.zoom)
	p.tl.ClipMenu = p.clipMenuItems
	p.applyTimelinePrefs()
	p.timelineHost = container.NewStack(p.tl)
	p.bar = newToolbar(p)
}

func (p *Page) buildLibrary()   { p.lib = newLibrary(p) }
func (p *Page) buildInspector() { p.insp = newInspector(p) }

// attachWindow lets the timeline accept media dragged from the library (once the
// window is known).
func (p *Page) attachWindow() {
	w := p.ctx.Window
	if w == nil || w == p.attached || p.tl == nil {
		return
	}
	if a, ok := any(p.tl).(windowAttacher); ok {
		a.AttachWindow(w)
		p.attached = w
	}
}

func (p *Page) changed(c session.Change) {
	if p.closed {
		return
	}
	if c&(session.ChangeProject|session.ChangeSelection) != 0 {
		if p.lib != nil {
			p.lib.update(c)
		}
		if p.insp != nil {
			p.insp.update()
		}
	}
	if p.bar != nil && c&(session.ChangeProject|session.ChangeSelection|session.ChangeMeta) != 0 {
		p.bar.update()
	}
	if p.progName != nil && c&(session.ChangeProject|session.ChangeMeta) != 0 {
		pr := p.ctx.Session.Project
		setText(p.progName, pr.Name)
		setText(p.progMeta, fmt.Sprintf("%d×%d  %s", pr.Settings.Width, pr.Settings.Height, pr.Rate().Label()))
	}
	if c&session.ChangeProject != 0 && p.root != nil {
		p.root.Refresh() // track count may change the timeline height
	}
}

func (p *Page) Activate() {
	p.active = true
	p.setViewerActive()
	if p.transport != nil && p.transport.Meter() != nil {
		p.transport.Meter().SetActive(true)
	}
	if p.lib != nil {
		p.lib.retryThumbs()
		p.lib.checkOffline()
	}
	p.attachWindow()
}

func (p *Page) Deactivate() {
	p.active = false
	p.exitFullscreen()
	p.setViewerActive()
	if p.transport != nil && p.transport.Meter() != nil {
		p.transport.Meter().SetActive(false) // no 30 Hz polling for a hidden meter
	}
}

// setViewerActive renders only while the page is shown and the viewer is not hidden
// (or fills the window).
func (p *Page) setViewerActive() {
	if p.viewer == nil {
		return
	}
	on := p.active || p.panel != ""
	if p.panel == "" && p.hidden[areaViewer] && p.fs == nil {
		on = false
	}
	if on != p.viewer.Active() {
		p.viewer.SetActive(on)
	}
}

func (p *Page) Close() {
	if p.stopLife != nil {
		p.stopLife()
	}
	if p.closed {
		return
	}
	p.closed = true
	p.exitFullscreen()
	for _, u := range p.unsubs {
		u()
	}
	p.unsubs = nil
	if p.st != nil {
		releaseShared(p)
	}
	if p.ownReg {
		p.reg.Unbind(command.Global)
	}
	if p.unsub != nil {
		p.unsub()
	}
	if p.lib != nil {
		p.lib.close()
	}
	if p.tl != nil {
		p.tl.Close()
	}
	if p.cacheStop != nil {
		close(p.cacheStop)
	}
	if p.statsStop != nil {
		p.statsStop()
	}
	if p.viewer != nil {
		p.viewer.Close()
	}
	if p.transport != nil {
		p.transport.Close()
	}
}

// SelectAll selects every clip on unlocked tracks. Ctrl+A is the command edit.selectAll;
// this is kept for direct callers.
func (p *Page) SelectAll() {
	if p.started {
		p.ctx.Session.SelectAll()
	}
}

// TypedKey handles keys while no widget has focus. Keys of the page's commands run them
// with the user's key map (the shell normally dispatches them through the registry
// first); keyboard-layout aliases ("+" on "]", Rücktaste for Entf) and Enter in the
// library are handled here. Playback keys are global commands of the shell.
func (p *Page) TypedKey(k *fyne.KeyEvent) bool {
	if !p.started || p.closed {
		return false
	}
	name := k.Name
	switch name {
	case fyne.KeyBackspace:
		name = fyne.KeyDelete
	case fyne.KeyEqual, fyne.KeyRightBracket: // "+" sits on "]" in German layouts
		name = fyne.KeyPlus
	case fyne.KeySlash: // "-" sits on "/" in German layouts
		name = fyne.KeyMinus
	}
	mods := heldModifiers()
	if p.dispatch(name, mods) {
		return true
	}
	if name == fyne.KeyMinus || name == fyne.KeyPlus {
		// the plain key on layouts that need Shift for it
		if p.dispatch(name, mods&^fyne.KeyModifierShift) {
			return true
		}
	}
	switch name {
	case fyne.KeyEscape:
		if p.fs != nil {
			p.exitFullscreen()
			return true
		}
	case fyne.KeyReturn, fyne.KeyEnter:
		if p.lib == nil {
			return false
		}
		if p.lib.mediaTabActive() {
			if len(p.lib.pickedIDs()) == 0 {
				return false
			}
			p.lib.insertMedia(false)
		} else {
			p.insertGenerator(generators[p.lib.gen].ID)
		}
		return true
	}
	return false
}

func (p *Page) showErr(err error) {
	if err != nil && p.ctx.ShowErr != nil {
		p.ctx.ShowErr(err)
	}
}

// edit runs one undoable edit and reports a failure.
func (p *Page) edit(label string, fn func(*model.Project) error) error {
	err := p.ctx.Session.Edit(label, fn)
	p.showErr(explain(err))
	return err
}

func (p *Page) setBladeAll(on bool) {
	if p.start(); p.st != nil { // commands may run before the content is built
		p.st.setBladeAll(on)
	}
}

// zoomChanged follows zoom changes the timeline made itself (Ctrl+wheel, pinch).
func (p *Page) zoomChanged(z float32) {
	if z == p.zoom {
		return
	}
	p.zoom = z
	p.bar.update()
	p.reg.Refresh()
}

// setTool selects the tool on every timeline of the project (page and panels).
func (p *Page) setTool(t timeline.Tool) {
	if p.start(); p.st != nil {
		p.st.setTool(t)
	}
}

// toggleSnap flips snapping in the settings, which every timeline follows.
func (p *Page) toggleSnap() {
	on := !p.snapping
	p.updateSettings(func(s *settings.Settings) { s.Timeline.Snapping = on })
}

func (p *Page) setZoom(z float32) {
	if p.tl == nil {
		return
	}
	p.tl.SetZoom(max(minZoom, min(maxZoom, z)))
	p.zoom = p.tl.Zoom()
	p.bar.update()
}

func (p *Page) zoomBy(f float32) { p.setZoom(p.zoom * f) }

// splitTarget returns the clip split cuts on the selected clip's track, or ok=false to
// cut every track. A selected clip on a locked track gives model.ErrLocked.
func (p *Page) splitTarget() (id string, ok bool, err error) {
	s := p.ctx.Session
	f := s.Playhead
	c, sel := s.Clip()
	if !sel {
		return "", false, nil
	}
	_, ti, _ := s.Project.Clip(c.ID)
	t := &s.Project.Tracks[ti]
	if t.Locked {
		return "", false, model.ErrLocked
	}
	if ci := t.ClipAt(f); ci >= 0 && t.Clips[ci].Start != f {
		return t.Clips[ci].ID, true, nil
	}
	return "", false, nil
}

// canSplitAll reports whether some unlocked track has a clip to cut at the playhead.
func (p *Page) canSplitAll() bool {
	s := p.ctx.Session
	for _, t := range s.Project.Tracks {
		if ci := t.ClipAt(s.Playhead); ci >= 0 && !t.Locked && t.Clips[ci].Start != s.Playhead {
			return true
		}
	}
	return false
}

// split cuts the clip under the playhead on the selected clip's track. Without a
// selection, or if that track has nothing to cut at the playhead, it cuts every
// unlocked track at the playhead. A selected clip on a locked track is reported instead
// of cutting the other tracks.
func (p *Page) split() {
	f := p.ctx.Session.Playhead
	id, ok, err := p.splitTarget()
	if err != nil {
		p.showErr(explain(err))
		return
	}
	if ok {
		p.edit("Clip teilen", func(pr *model.Project) error {
			_, err := pr.SplitClip(id, f)
			return err
		})
		return
	}
	p.splitAll()
}

// splitAll cuts every unlocked track at the playhead.
func (p *Page) splitAll() {
	f := p.ctx.Session.Playhead
	if !p.canSplitAll() {
		p.showErr(errors.New("Am Abspielkopf liegt kein Clip zum Teilen. Abspielkopf in einen Clip auf einer entsperrten Spur setzen"))
		return
	}
	p.edit("Alle Spuren teilen", func(pr *model.Project) error {
		pr.SplitAll(f)
		return nil
	})
}

// deleteSelected removes the selected clips; ripple closes the gaps (on every unlocked
// track, or only the edited ones when the sync lock is off in the settings).
func (p *Page) deleteSelected(ripple bool) bool {
	ids := p.selection()
	if len(ids) == 0 {
		return false
	}
	label := "Clip löschen"
	if len(ids) > 1 {
		label = fmt.Sprintf("%d Clips löschen", len(ids))
	}
	if ripple {
		label = "Löschen und Lücke schließen"
		sync := p.prefs.Timeline.RippleSyncLock
		p.edit(label, func(pr *model.Project) error { return timeline.RippleDelete(pr, ids, sync) })
		return true
	}
	p.edit(label, func(pr *model.Project) error { return pr.DeleteClips(ids, false) })
	return true
}

// selection returns every selected clip ID (the primary clip included).
func (p *Page) selection() []string {
	s := p.ctx.Session
	ids := append([]string(nil), s.Selected...)
	if s.SelectedClip != "" && !s.IsSelected(s.SelectedClip) {
		ids = append(ids, s.SelectedClip)
	}
	return ids
}

// markIn / markOut set the range; a point on the wrong side of the other clears that one.
func (p *Page) markIn() {
	s := p.ctx.Session
	if s.Out <= s.Playhead {
		s.Out = 0
	}
	s.SetIn(s.Playhead)
}

func (p *Page) markOut() {
	s := p.ctx.Session
	if s.Playhead <= s.In {
		s.In = 0
	}
	s.SetOut(s.Playhead)
}

func (p *Page) addTrack(kind model.TrackKind) {
	var id string
	label := "Videospur hinzufügen"
	if kind == model.TrackAudio {
		label = "Audiospur hinzufügen"
	}
	if p.edit(label, func(pr *model.Project) error {
		id = pr.AddTrack(kind).ID
		return nil
	}) == nil {
		p.ctx.Session.SelectTrack(id)
	}
}

// removeSelectedTrack deletes the selected track, asking first if it holds clips.
func (p *Page) removeSelectedTrack() {
	s := p.ctx.Session
	t, _ := s.Project.Track(s.SelectedTrack)
	if t == nil {
		p.showErr(errors.New("Keine Spur ausgewählt. Spur in der Timeline anklicken"))
		return
	}
	id, name, n := t.ID, t.Name, len(t.Clips)
	if n == 0 || p.ctx.Window == nil {
		p.removeTrack(id, name)
		return
	}
	d := dialog.NewConfirm("Spur löschen",
		fmt.Sprintf("Spur %s enthält %s. Spur mit allen Clips löschen?", name, plural(n, "Clip", "Clips")),
		func(ok bool) {
			if ok {
				p.removeTrack(id, name)
			}
		}, p.ctx.Window)
	d.SetConfirmText("Löschen")
	d.SetDismissText("Abbrechen")
	p.dlg = d
	d.Show()
}

func (p *Page) removeTrack(id, name string) {
	p.edit("Spur "+name+" löschen", func(pr *model.Project) error { return pr.RemoveTrack(id) })
}

// insertMedia places a media item at the playhead (or appends it) on a fitting track,
// selects the new clip and moves the playhead to its end.
func (p *Page) insertMedia(mediaID string, atEnd bool) { p.insertMediaList([]string{mediaID}, atEnd) }

// insertMediaList places media items one after another at the playhead (or the end) in
// one undo step, selects the new clips and moves the playhead to the end of the last.
func (p *Page) insertMediaList(mediaIDs []string, atEnd bool) {
	s := p.ctx.Session
	var items []model.MediaItem
	for _, mid := range mediaIDs {
		m, ok := s.Project.MediaByID(mid)
		if !ok {
			p.showErr(errors.New("Das Medium ist nicht mehr im Projekt. Liste aktualisieren und erneut wählen"))
			return
		}
		items = append(items, *m)
	}
	if len(items) == 0 {
		return
	}
	start := s.Playhead
	if atEnd {
		start = s.Project.Duration()
	}
	var ids []string
	label := "Clip einfügen"
	if atEnd {
		label = "Clip anhängen"
	}
	if len(items) > 1 {
		label = fmt.Sprintf("%d Clips einfügen", len(items))
		if atEnd {
			label = fmt.Sprintf("%d Clips anhängen", len(items))
		}
	}
	still := p.prefs.Timeline.StillSeconds
	if p.edit(label, func(pr *model.Project) error {
		at := start
		for _, item := range items {
			id, err := insertMedia(pr, s.SelectedTrack, item, at, still)
			if err != nil {
				return err
			}
			ids = append(ids, id)
			if c, _, _ := pr.Clip(id); c != nil {
				at = c.End()
			}
		}
		return nil
	}) == nil {
		s.SelectClips(ids)
		if c, _, _ := s.Project.Clip(ids[len(ids)-1]); c != nil {
			s.Seek(c.End())
		}
	}
}

// insertGenerator places a generator clip (settings Timeline.GeneratorSecs long) at the
// playhead; titles go on a free track above the picture (see placeGenerator).
func (p *Page) insertGenerator(genID string) {
	s := p.ctx.Session
	c := generatorClip(genID, s.FPS(), s.Playhead, p.prefs.Timeline.GeneratorSecs)
	if p.edit(c.Name+" einfügen", func(pr *model.Project) error {
		return placeGenerator(pr, s.SelectedTrack, c)
	}) == nil {
		p.selectInserted(c.ID)
	}
}

func (p *Page) selectInserted(id string) {
	s := p.ctx.Session
	s.SelectClip(id)
	if c, _, _ := s.Project.Clip(id); c != nil {
		s.Seek(c.End())
	}
}

// setFPS changes the frame rate in one undo step, keeping clip timing in seconds. The
// session moves playhead and In/Out along, also on undo and redo.
func (p *Page) setFPS(fps model.Rate) {
	if fps.Equal(p.ctx.Session.FPS()) {
		return
	}
	st := p.ctx.Session.Project.Settings
	st.FPS = fps
	p.edit("Bildrate ändern", func(pr *model.Project) error { return pr.SetSettings(st) })
}
