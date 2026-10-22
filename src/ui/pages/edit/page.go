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
	viewer       *viewer.Viewer
	tl           *timeline.Timeline
	timelineHost *fyne.Container
	lib          *library
	insp         *inspector
	bar          *toolbar
	progName     *canvas.Text
	progMeta     *canvas.Text

	tool     timeline.Tool
	snapping bool
	zoom     float32

	unsub  func()
	closed bool
}

func New(ctx *page.Context) page.Page {
	return &Page{ctx: ctx, post: kit.Do, snapping: true, zoom: 1}
}

func (p *Page) Content() fyne.CanvasObject {
	if p.root != nil {
		return p.root
	}
	s := p.ctx.Session
	p.viewer = viewer.New(s, p.ctx.Renderer)
	p.viewer.SetDeliver(p.post)
	p.tl = timeline.New(s, p.ctx.Media)
	p.tl.OnError = func(err error) { p.showErr(explain(err)) }
	p.tl.SetTool(p.tool)
	p.tl.SetSnapping(p.snapping)
	p.tl.SetZoom(p.zoom)
	p.timelineHost = container.NewStack(p.tl)

	p.lib = newLibrary(p)
	p.insp = newInspector(p)
	p.bar = newToolbar(p)

	p.progName = text("", studiotheme.Text, 12)
	p.progMeta = kit.MonoText("", studiotheme.Faint, 11)
	progInfo := container.NewHBox(container.NewCenter(p.progName), container.NewCenter(p.progMeta), fixed(4, 0))
	program := kit.Panel("Programm", progInfo,
		container.NewBorder(nil, viewer.Transport(s), nil, nil, p.viewer))

	right := container.NewHSplit(program, kit.Panel("Inspector", container.NewHBox(p.insp.kind, fixed(6, 0)), p.insp.content))
	right.Offset = 0.7
	top := container.NewHSplit(kit.Panel("Bibliothek", nil, p.lib.content), right)
	top.Offset = 0.2
	root := container.NewVSplit(top, kit.Panel("Timeline", p.bar.content, p.timelineHost))
	root.Offset = 0.6
	p.root = root

	p.unsub = s.Subscribe(p.changed)
	p.changed(session.ChangeProject | session.ChangeSelection | session.ChangeMeta)
	return p.root
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
}

func (p *Page) Activate() {
	if p.viewer != nil {
		p.viewer.SetActive(true)
	}
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
	p.unsub()
	p.tl.Close()
	p.viewer.Close()
}

// TypedKey handles the edit page shortcuts (JKL, arrows, I/O/M, A/B/S, Entf, +/-).
func (p *Page) TypedKey(k *fyne.KeyEvent) bool {
	if p.root == nil {
		return false
	}
	s := p.ctx.Session
	switch k.Name {
	case fyne.KeySpace:
		s.TogglePlaying()
	case fyne.KeyJ, fyne.KeyUp:
		s.Seek(s.PrevEdit())
	case fyne.KeyK:
		s.SetPlaying(false)
	case fyne.KeyL:
		s.SetPlaying(true)
	case fyne.KeyLeft:
		s.Seek(s.Playhead - 1)
	case fyne.KeyRight:
		s.Seek(s.Playhead + 1)
	case fyne.KeyDown:
		s.Seek(s.NextEdit())
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
	case fyne.KeyS:
		p.split()
	case fyne.KeyDelete, fyne.KeyBackspace:
		return p.deleteSelected(false)
	case fyne.KeyPlus, fyne.KeyEqual, fyne.KeyRightBracket: // "+" sits on "]" in German layouts
		p.zoomBy(1.5)
	case fyne.KeyMinus, fyne.KeySlash: // "-" sits on "/" in German layouts
		p.zoomBy(1 / 1.5)
	case fyne.KeyReturn, fyne.KeyEnter:
		if p.lib.mediaTabActive() {
			if s.SelectedMedia == "" {
				return false
			}
			p.insertMedia(s.SelectedMedia, false)
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
	p.zoom = max(minZoom, min(maxZoom, z))
	p.tl.SetZoom(p.zoom)
	p.bar.update()
}

func (p *Page) zoomBy(f float32) { p.setZoom(p.zoom * f) }

// split cuts the selected clip's track at the playhead, or every track without selection.
func (p *Page) split() {
	s := p.ctx.Session
	f := s.Playhead
	if c, ok := s.Clip(); ok {
		_, ti, _ := s.Project.Clip(c.ID)
		t := &s.Project.Tracks[ti]
		ci := t.ClipAt(f)
		if ci < 0 || t.Clips[ci].Start == f {
			p.showErr(fmt.Errorf("Auf Spur %s liegt am Playhead kein Clip zum Teilen. Playhead in einen Clip setzen oder die Auswahl mit Klick auf eine leere Stelle aufheben", t.Name))
			return
		}
		id := t.Clips[ci].ID
		p.edit("Clip teilen", func(pr *model.Project) error {
			_, err := pr.SplitClip(id, f)
			return err
		})
		return
	}
	cut := false
	for _, t := range s.Project.Tracks {
		if ci := t.ClipAt(f); ci >= 0 && !t.Locked && t.Clips[ci].Start != f {
			cut = true
		}
	}
	if !cut {
		p.showErr(errors.New("Am Playhead liegt kein Clip zum Teilen. Playhead in einen Clip setzen"))
		return
	}
	p.edit("Alle Spuren teilen", func(pr *model.Project) error {
		pr.SplitAll(f)
		return nil
	})
}

// deleteSelected removes the selected clip, closing the gap with ripple.
func (p *Page) deleteSelected(ripple bool) bool {
	id := p.ctx.Session.SelectedClip
	if id == "" {
		return false
	}
	label := "Clip löschen"
	if ripple {
		label = "Löschen und Lücke schließen"
	}
	p.edit(label, func(pr *model.Project) error { return pr.DeleteClip(id, ripple) })
	return true
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
func (p *Page) insertMedia(mediaID string, atEnd bool) {
	s := p.ctx.Session
	m, ok := s.Project.MediaByID(mediaID)
	if !ok {
		p.showErr(errors.New("Das Medium ist nicht mehr im Projekt. Liste aktualisieren und erneut wählen"))
		return
	}
	item := *m
	start := s.Playhead
	if atEnd {
		start = s.Project.Duration()
	}
	var id string
	label := "Clip einfügen"
	if atEnd {
		label = "Clip anhängen"
	}
	if p.edit(label, func(pr *model.Project) (err error) {
		id, err = insertMedia(pr, s.SelectedTrack, item, start)
		return err
	}) == nil {
		p.selectInserted(id)
	}
}

// insertGenerator places a 5 s generator clip at the playhead on the selected video track.
func (p *Page) insertGenerator(genID string) {
	s := p.ctx.Session
	c := generatorClip(genID, s.FPS(), s.Playhead)
	if p.edit(c.Name+" einfügen", func(pr *model.Project) error {
		t, err := targetTrack(pr, s.SelectedTrack, c.Kind)
		if err != nil {
			return err
		}
		return pr.PlaceClip(t.ID, c)
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

// setFPS changes the frame rate in one undo step, keeping clip timing in seconds.
func (p *Page) setFPS(fps int) {
	s := p.ctx.Session
	old := s.FPS()
	if fps == old {
		return
	}
	if p.edit("Bildrate ändern", func(pr *model.Project) error { return rescaleFPS(pr, fps) }) == nil {
		rescaleSession(s, old, fps)
	}
}
