// Package edit is the "edit" workspace page.
//
// OWNER: edit page agent. New(ctx) is the contract; do not change its signature.
package edit

import (
	"errors"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/timeline"
	"framestudio-proto/src/ui/viewer"
)

// Page is the edit workspace: library, program monitor, inspector and timeline.
type Page struct {
	ctx  *page.Context
	post func(func()) // hands background results to the UI thread

	root         fyne.CanvasObject
	vsplit       *container.Split // monitor row over timeline
	hsplit       *container.Split // library | program and inspector
	rsplit       *container.Split // program | inspector
	viewer       *viewer.Viewer
	transport    *viewer.TransportBar
	tl           *timeline.Timeline
	timelineHost *fyne.Container
	lib          *library
	insp         *inspector
	bar          *toolbar
	progName     *canvas.Text
	progMeta     *canvas.Text

	tool     timeline.Tool
	snapping bool
	bladeAll bool
	zoom     float32

	attached fyne.Window // window the timeline was attached to for media drops
	unsub    func()
	closed   bool
}

func New(ctx *page.Context) page.Page {
	return &Page{ctx: ctx, post: kit.Do, snapping: true, zoom: 1}
}

// windowAttacher is implemented by timelines that accept media drags from the library
// (timeline.AttachWindow). Asserted so the page builds with and without it.
type windowAttacher interface{ AttachWindow(fyne.Window) }

func (p *Page) Content() fyne.CanvasObject {
	if p.root != nil {
		return p.root
	}
	s := p.ctx.Session
	p.viewer = viewer.New(s, p.ctx.Renderer)
	p.viewer.SetDeliver(p.post)
	p.transport = viewer.NewTransportBar(s, true)
	p.tl = timeline.New(s, p.ctx.Media)
	p.tl.OnError = func(err error) { p.showErr(explain(err)) }
	p.tl.OnZoomChanged = p.zoomChanged
	p.tl.SetTool(p.tool)
	p.tl.SetSnapping(p.snapping)
	p.tl.SetBladeAllTracks(p.bladeAll)
	p.tl.SetZoom(p.zoom)
	p.timelineHost = container.NewStack(p.tl)

	p.lib = newLibrary(p)
	p.insp = newInspector(p)
	p.bar = newToolbar(p)

	p.progName = text("", studiotheme.Text, 12)
	p.progMeta = kit.MonoText("", studiotheme.Faint, 11)
	progInfo := container.NewHBox(container.NewCenter(p.progName), container.NewCenter(p.progMeta), fixed(4, 0))
	program := kit.Panel("Programm", progInfo,
		container.NewBorder(nil, p.transport.Content(), nil, nil, p.viewer))

	p.rsplit = container.NewHSplit(program, kit.Panel("Inspector", container.NewHBox(p.insp.kind, fixed(6, 0)), p.insp.content))
	p.rsplit.Offset = 0.7
	p.hsplit = container.NewHSplit(kit.Panel("Bibliothek", nil, p.lib.content), p.rsplit)
	p.hsplit.Offset = 0.2
	p.vsplit = container.NewVSplit(p.hsplit, kit.Panel("Timeline", p.bar.content, p.timelineHost))
	p.vsplit.Offset = 0.6
	p.root = container.New(&fitTimeline{pg: p}, p.vsplit)

	p.unsub = s.Subscribe(p.changed)
	p.changed(session.ChangeProject | session.ChangeSelection | session.ChangeMeta)
	p.attachWindow()
	return p.root
}

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
	if c&(session.ChangeProject|session.ChangeSelection) != 0 {
		p.lib.update(c)
		p.insp.update()
	}
	if c&(session.ChangeProject|session.ChangeSelection|session.ChangePlayhead|session.ChangeMeta) != 0 {
		p.bar.update()
	}
	if c&(session.ChangeProject|session.ChangeMeta) != 0 {
		pr := p.ctx.Session.Project
		setText(p.progName, pr.Name)
		setText(p.progMeta, fmt.Sprintf("%d×%d  %d fps", pr.Settings.Width, pr.Settings.Height, pr.Settings.FPS))
	}
	if c&session.ChangeProject != 0 && p.root != nil {
		p.root.Refresh() // track count may change the timeline height
	}
}

func (p *Page) Activate() {
	if p.viewer != nil {
		p.viewer.SetActive(true)
	}
	if p.lib != nil {
		p.lib.retryThumbs()
		p.lib.checkOffline()
	}
	p.attachWindow()
}

func (p *Page) Deactivate() {
	if p.viewer != nil {
		p.viewer.SetActive(false)
	}
}

func (p *Page) Close() {
	if p.closed || p.root == nil {
		p.closed = true
		return
	}
	p.closed = true
	p.lib.close()
	p.unsub()
	p.tl.Close()
	p.viewer.Close()
	p.transport.Close()
}

// SelectAll selects every clip on unlocked tracks. Ctrl+A is owned by the shell, which
// calls Session.SelectAll for the active project; this is kept for direct callers.
func (p *Page) SelectAll() {
	if p.root != nil {
		p.ctx.Session.SelectAll()
	}
}

// TypedKey handles the edit page shortcuts; see the key map in keymap.go.
func (p *Page) TypedKey(k *fyne.KeyEvent) bool {
	if p.root == nil {
		return false
	}
	s := p.ctx.Session
	switch k.Name {
	case fyne.KeySpace:
		s.TogglePlaying()
	case fyne.KeyJ:
		s.Shuttle(-1)
	case fyne.KeyK:
		s.Shuttle(0)
	case fyne.KeyL:
		s.Shuttle(+1)
	case fyne.KeyUp:
		s.Seek(s.PrevEdit())
	case fyne.KeyDown:
		s.Seek(s.NextEdit())
	case fyne.KeyLeft:
		s.Seek(s.Playhead - 1)
	case fyne.KeyRight:
		s.Seek(s.Playhead + 1)
	case fyne.KeyHome:
		s.Seek(0)
	case fyne.KeyEnd:
		s.Seek(s.Project.Duration())
	case fyne.KeyI:
		p.markIn()
	case fyne.KeyO:
		p.markOut()
	case fyne.KeyM:
		p.addMarker()
	case fyne.KeyA:
		p.setTool(timeline.ToolSelect)
	case fyne.KeyB:
		p.setTool(timeline.ToolBlade)
	case fyne.KeyR:
		p.setTool(timeline.ToolRipple)
	case fyne.KeyN:
		p.setTool(timeline.ToolRoll)
	case fyne.KeyS:
		p.split()
	case fyne.KeyEscape:
		if kit.MediaDragActive() {
			kit.CancelMediaDrag()
			return true
		}
		// The timeline cancels a rename, marquee or drag first, else clears the selection.
		return p.tl.Escape()
	case fyne.KeyDelete, fyne.KeyBackspace:
		return p.deleteSelected(false)
	case fyne.KeyPlus, fyne.KeyEqual, fyne.KeyRightBracket: // "+" sits on "]" in German layouts
		p.zoomBy(1.5)
	case fyne.KeyMinus, fyne.KeySlash: // "-" sits on "/" in German layouts
		p.zoomBy(1 / 1.5)
	case fyne.KeyReturn, fyne.KeyEnter:
		if p.lib.mediaTabActive() {
			if len(p.lib.pickedIDs()) == 0 {
				return false
			}
			p.lib.insertMedia(false)
		} else {
			p.insertGenerator(generators[p.lib.gen].ID)
		}
	default:
		return false
	}
	return true
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
	p.bladeAll = on
	p.tl.SetBladeAllTracks(on)
	p.bar.update()
}

// zoomChanged follows zoom changes the timeline made itself (Ctrl+wheel, pinch).
func (p *Page) zoomChanged(z float32) {
	if z == p.zoom {
		return
	}
	p.zoom = z
	p.bar.update()
}

func (p *Page) setTool(t timeline.Tool) {
	p.tool = t
	p.tl.SetTool(t)
	p.bar.update()
}

func (p *Page) toggleSnap() {
	p.snapping = !p.snapping
	p.tl.SetSnapping(p.snapping)
	p.bar.update()
}

func (p *Page) setZoom(z float32) {
	p.tl.SetZoom(max(minZoom, min(maxZoom, z)))
	p.zoom = p.tl.Zoom()
	p.bar.update()
}

func (p *Page) zoomBy(f float32) { p.setZoom(p.zoom * f) }

// split cuts the clip under the playhead on the selected clip's track. Without a
// selection, or if that track has nothing to cut at the playhead, it cuts every
// unlocked track at the playhead.
func (p *Page) split() {
	s := p.ctx.Session
	f := s.Playhead
	if c, ok := s.Clip(); ok {
		_, ti, _ := s.Project.Clip(c.ID)
		t := &s.Project.Tracks[ti]
		if ci := t.ClipAt(f); ci >= 0 && t.Clips[ci].Start != f && !t.Locked {
			id := t.Clips[ci].ID
			p.edit("Clip teilen", func(pr *model.Project) error {
				_, err := pr.SplitClip(id, f)
				return err
			})
			return
		}
	}
	cut := false
	for _, t := range s.Project.Tracks {
		if ci := t.ClipAt(f); ci >= 0 && !t.Locked && t.Clips[ci].Start != f {
			cut = true
		}
	}
	if !cut {
		p.showErr(errors.New("Am Playhead liegt kein Clip zum Teilen. Playhead in einen Clip auf einer entsperrten Spur setzen"))
		return
	}
	p.edit("Alle Spuren teilen", func(pr *model.Project) error {
		pr.SplitAll(f)
		return nil
	})
}

// deleteSelected removes the selected clip, closing the gap with ripple.
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
	}
	p.edit(label, func(pr *model.Project) error { return pr.DeleteClips(ids, ripple) })
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

func (p *Page) addMarker() {
	f := p.ctx.Session.Playhead
	p.edit("Marker setzen", func(pr *model.Project) error {
		pr.AddMarker(f, fmt.Sprintf("Marker %d", len(pr.Markers)+1))
		return nil
	})
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
	dialog.ShowConfirm("Spur löschen",
		fmt.Sprintf("Spur %s enthält %s. Spur mit allen Clips löschen?", name, plural(n, "Clip", "Clips")),
		func(ok bool) {
			if ok {
				p.removeTrack(id, name)
			}
		}, p.ctx.Window)
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
	if p.edit(label, func(pr *model.Project) error {
		at := start
		for _, item := range items {
			id, err := insertMedia(pr, s.SelectedTrack, item, at)
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

// insertGenerator places a 5 s generator clip at the playhead; titles go on a free
// track above the picture (see placeGenerator).
func (p *Page) insertGenerator(genID string) {
	s := p.ctx.Session
	c := generatorClip(genID, s.FPS(), s.Playhead)
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
func (p *Page) setFPS(fps int) {
	if fps == p.ctx.Session.FPS() {
		return
	}
	p.edit("Bildrate ändern", func(pr *model.Project) error { return rescaleFPS(pr, fps) })
}

// tlMaxPart caps the timeline panel's share of the page height; below it the panel is
// sized so every track shows without scrolling (Timeline.ContentHeight).
const tlMaxPart = 0.55

// Preferred panel widths: the library and inspector keep a usable width, the program
// monitor takes the rest.
const (
	libFrac, libMinW, libMaxW    = 0.2, 240, 300
	inspFrac, inspMinW, inspMaxW = 0.25, 300, 380
)

// fitTimeline sizes the page splits for the window: the timeline panel shows all tracks
// and library and inspector get a usable width. Each split follows the window until the
// user drags its divider.
type fitTimeline struct {
	pg              *Page
	vert, lib, insp autoOffset
}

// autoOffset sets a split offset until the user moves the divider.
type autoOffset struct {
	set    float64 // offset last chosen here; a different value means the user dragged
	manual bool
}

func (a *autoOffset) apply(sp *container.Split, off float64) {
	if a.set != 0 && sp.Offset != a.set {
		a.manual = true
	}
	if a.manual {
		return
	}
	off = max(0.05, min(0.95, off))
	if off != sp.Offset {
		sp.Offset = off
		sp.Refresh()
	}
	a.set = sp.Offset
}

// splitGap returns the thickness of a split's divider, or the theme padding before the
// first layout.
func splitGap(sp *container.Split, total float32, horizontal bool) float32 {
	a, b := sp.Leading.Size(), sp.Trailing.Size()
	d := total - a.Height - b.Height
	if horizontal {
		d = total - a.Width - b.Width
	}
	if a.IsZero() || d < 0 || d > 40 {
		return theme.Padding()
	}
	return d
}

func (l *fitTimeline) wantHeight() float32 {
	h := l.pg.tl.ContentHeight() + 4
	bottom := l.pg.vsplit.Trailing
	// panel chrome = panel min height minus the timeline's own min height
	chrome := bottom.MinSize().Height - l.pg.timelineHost.MinSize().Height
	return h + chrome
}

func (l *fitTimeline) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.Position{})
		o.Resize(size)
	}
	if size.Height < 1 || size.Width < 1 {
		return
	}
	pg := l.pg
	avail := float64(size.Height - splitGap(pg.vsplit, size.Height, false))
	want := float64(min(l.wantHeight(), size.Height*tlMaxPart))
	l.vert.apply(pg.vsplit, 1-want/avail)

	w := float64(size.Width)
	libW := max(libMinW, min(libMaxW, libFrac*w))
	l.lib.apply(pg.hsplit, libW/(w-float64(splitGap(pg.hsplit, size.Width, true))))
	rest := float64(pg.rsplit.Size().Width)
	if rest < 1 {
		rest = w - libW
	}
	inspW := max(inspMinW, min(inspMaxW, inspFrac*w))
	l.insp.apply(pg.rsplit, 1-inspW/(rest-float64(splitGap(pg.rsplit, float32(rest), true))))
}

func (l *fitTimeline) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return objs[0].MinSize()
}
