// Package audio is the "audio" workspace page.
//
// OWNER: audio page agent. New(ctx) is the contract; do not change its signature.
//
// The page follows Resolve's Fairlight layout: zoomable audio lanes with waveforms on top,
// the selected clips and a metered channel-strip mixer below. Sound comes from the live
// engine (liveaudio) during normal playback; "Vorhören" plays the In/Out range (or from
// the playhead) through it, or mixes a WAV for an external player when live sound is
// not available.
package audio

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os/exec"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynelayout "fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/liveaudio"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

var errLockedClip = errors.New("Die Spur des Clips ist gesperrt. Im Spurkopf das Schloss lösen und erneut versuchen.")

// waveformSource is the part of *media.Cache the page needs; tests substitute it.
type waveformSource interface {
	Waveform(m model.MediaItem, buckets int) ([]float32, error)
}

// waveEntry is a cached waveform of one media item.
type waveEntry struct {
	peaks   []float32
	err     error
	loading bool
}

type Page struct {
	ctx   *page.Context
	s     *session.Session
	waves waveformSource
	spawn func(func()) // starts background work; tests replace it to run jobs deterministically
	peaks map[string]*waveEntry

	// levels reads the live meter levels; clock calls tick at display rate until stopped.
	// Tests replace both.
	levels func(*session.Session) (liveaudio.Level, map[string]liveaudio.Level)
	clock  func(tick func()) (stop func())
	now    func() time.Time
	// mods returns the keyboard modifiers held right now (Ctrl+click, Ctrl+wheel).
	mods func() fyne.KeyModifier

	view      viewport
	meterStop func()
	meterGen  int
	meterLast time.Time

	prev    *previewer
	tracks  *trackView
	mixer   *mixer
	clip    *clipPanel
	zoom    *kit.Slider
	play    *widget.Button
	stop    *widget.Button
	status  *canvas.Text
	content fyne.CanvasObject
	unsub   func()
	active  bool
	stale   bool
	closed  bool

	// mixerOnly builds just the mixer (the detachable mixer panel, see panel.go).
	mixerOnly bool
	main      *container.Split // lanes / bottom row
	bottom    *container.Split // clip | mixer
	clipBox   fyne.CanvasObject
	mixerBox  fyne.CanvasObject
	// pending is a layout applied before Content was built.
	pending *layout.Page
}

func New(ctx *page.Context) page.Page {
	p := &Page{ctx: ctx, s: ctx.Session, active: true, peaks: map[string]*waveEntry{},
		spawn: func(f func()) { go f() }, levels: liveaudio.Levels, clock: tickerClock, now: time.Now,
		mods: currentModifiers, view: viewport{zoom: 1}}
	if ctx.Media != nil {
		p.waves = ctx.Media
	}
	p.prev = &previewer{
		s: p.s,
		run: func(c context.Context, pr *model.Project, o export.Options) error {
			return export.Run(c, pr, ctx.Renderer, o, func(export.Progress) {})
		},
		lookPath: exec.LookPath,
		spawn:    func(f func()) { p.spawn(f) },
		onChange: p.updateTransport,
		onError:  p.showErr,
	}
	// Vorhören goes through session playback when the live engine sounds this session and
	// has its tools; otherwise it mixes a WAV for an external player and holds the engine.
	p.prev.live = func() bool { return liveaudio.Active(p.ctx.Session) && p.prev.liveAvailable() }
	return p
}

// currentModifiers asks the desktop driver which modifier keys are held.
func currentModifiers() fyne.KeyModifier {
	if a := fyne.CurrentApp(); a != nil {
		if d, ok := a.Driver().(desktop.Driver); ok {
			return d.CurrentKeyModifiers()
		}
	}
	return 0
}

// ctrlHeld reports whether Ctrl (or Cmd) is held.
func (p *Page) ctrlHeld() bool {
	return p.mods()&(fyne.KeyModifierControl|fyne.KeyModifierSuper) != 0
}

func (p *Page) shiftHeld() bool { return p.mods()&fyne.KeyModifierShift != 0 }

func (p *Page) Content() fyne.CanvasObject {
	if p.content != nil {
		return p.content
	}
	if p.mixerOnly {
		return p.mixerContent()
	}
	p.tracks = newTrackView(p)
	p.mixer = newMixer(p)
	p.clip = newClipPanel(p)

	p.status = canvas.NewText("", studiotheme.Muted)
	p.status.TextSize = 11
	p.play = widget.NewButtonWithIcon("Vorhören", theme.MediaPlayIcon(), p.prev.Start)
	p.play.Importance = widget.LowImportance
	p.stop = widget.NewButtonWithIcon("Stopp", theme.MediaStopIcon(), p.prev.Stop)
	p.stop.Importance = widget.LowImportance

	p.zoom = kit.NewSlider(0, 1)
	p.zoom.Step = 0.001
	p.zoom.OnChanged = func(v float64) { p.setZoom(sliderToZoom(v, maxZoom(p.span(), p.s.FPS())), -1) }
	zoomOut := kit.ToolButton(theme.ZoomOutIcon(), func() { p.setZoom(p.view.zoom/1.5, -1) })
	zoomIn := kit.ToolButton(theme.ZoomInIcon(), func() { p.setZoom(p.view.zoom*1.5, -1) })
	fit := widget.NewButton("Einpassen", func() { p.setZoom(1, -1) })
	fit.Importance = widget.LowImportance
	zoomBox := container.NewHBox(zoomOut, container.NewGridWrap(fyne.NewSize(120, 30), p.zoom), zoomIn, fit)

	sep := canvas.NewRectangle(studiotheme.Border)
	sep.SetMinSize(fyne.NewSize(1, 18))
	tools := container.NewHBox(zoomBox, container.NewCenter(sep), fixedSize(4, 0),
		container.NewCenter(p.status), fixedSize(4, 0), p.play, p.stop)

	add := widget.NewButtonWithIcon("Spur hinzufügen", theme.ContentAddIcon(), p.addTrack)
	add.Importance = widget.LowImportance

	top := kit.Panel("Spuren", tools, p.tracks.obj)
	p.clipBox = kit.Panel("Clip", nil, p.clip.obj)
	p.mixerBox = kit.Panel("Mixer", add, p.mixer.obj)
	bottom := container.NewHSplit(p.clipBox, p.mixerBox)
	bottom.Offset = defaultBottomSplit
	split := container.NewVSplit(top, bottom)
	split.Offset = defaultMainSplit
	p.main, p.bottom = split, bottom
	p.content = container.NewStack(canvas.NewRectangle(studiotheme.Gap),
		container.New(fynelayout.NewCustomPaddedLayout(4, 4, 4, 4), split))

	p.unsub = p.s.Subscribe(p.changed)
	p.refresh()
	if p.pending != nil {
		p.ApplyLayout(*p.pending)
		p.pending = nil
	}
	return p.content
}

func (p *Page) Activate() {
	p.active = true
	if p.stale && p.content != nil {
		p.refresh()
	}
	p.syncMeters()
}

// Deactivate stops a running preview (its Stopp button is not reachable from other
// pages) and the meters.
func (p *Page) Deactivate() {
	p.active = false
	p.prev.Stop()
	p.stopMeters()
	if p.mixer != nil {
		p.mixer.resetMeters()
	}
}

// TypedKey: Space toggles the preview, J/K/L shuttle, Left/Right step one frame, Home/End jump.
// With a command registry (the shell) these keys are left to it: play.toggle is bound to
// Vorhören in this page's scope and the global transport commands do the rest, so the
// user's key map and modifiers (Umschalt+Links, Umschalt+Leertaste) apply.
func (p *Page) TypedKey(k *fyne.KeyEvent) bool {
	if p.ctx != nil && p.ctx.Commands != nil {
		return false
	}
	switch k.Name {
	case fyne.KeySpace:
		p.togglePreview()
	case fyne.KeyJ:
		p.s.Shuttle(-1)
	case fyne.KeyK:
		p.s.Shuttle(0)
	case fyne.KeyL:
		p.s.Shuttle(1)
	case fyne.KeyLeft:
		p.s.Seek(p.s.Playhead - 1)
	case fyne.KeyRight:
		p.s.Seek(p.s.Playhead + 1)
	case fyne.KeyHome:
		p.s.Seek(0)
	case fyne.KeyEnd:
		p.s.Seek(p.s.Project.Duration())
	default:
		return false
	}
	return true
}

// Wait lets a running preview remove its temporary WAV and its player exit.
func (p *Page) Wait(timeout time.Duration) bool { return p.prev.wait(timeout) }

func (p *Page) Close() {
	p.closed = true
	p.prev.Stop()
	p.stopMeters()
	if p.unsub != nil {
		p.unsub()
		p.unsub = nil
	}
}

func (p *Page) changed(c session.Change) {
	p.prev.sessionChanged(c)
	if !p.active {
		p.stale = true
		return
	}
	if c&session.ChangePlayback != 0 {
		p.syncMeters()
	}
	if c&(session.ChangeProject|session.ChangeSelection) != 0 {
		p.refresh()
		return
	}
	if c&session.ChangePlayhead != 0 {
		p.followPlayhead()
		if p.tracks != nil {
			p.tracks.updatePlayhead()
		}
		p.updateTransport()
	}
}

func (p *Page) refresh() {
	p.stale = false
	p.view.clamp(p.span(), p.s.FPS())
	p.syncZoomSlider()
	if p.tracks != nil {
		p.tracks.update()
	}
	p.mixer.update()
	if p.clip != nil {
		p.clip.update()
	}
	p.updateTransport()
}

func (p *Page) updateTransport() {
	if p.content == nil || p.play == nil {
		p.syncMeters()
		return
	}
	busy := p.prev.Busy()
	text := p.prev.status
	if !busy {
		text = p.prev.idleStatus()
	}
	setText(p.status, text)
	if busy {
		p.play.Importance = widget.HighImportance
		p.stop.Enable()
	} else {
		p.play.Importance = widget.LowImportance
		p.stop.Disable()
	}
	p.play.Refresh()
	p.syncMeters()
}

func (p *Page) showErr(err error) {
	if err == nil {
		return
	}
	if p.ctx.ShowErr != nil {
		p.ctx.ShowErr(err)
		return
	}
	log.Println("audio:", err)
}

// span is the number of frames the lanes scroll over: the program plus some headroom.
func (p *Page) span() int {
	fps := max(p.s.FPS(), 1)
	d := max(p.s.Project.Duration(), 10*fps)
	return d + d/16
}

func (p *Page) xOf(f int, w float32) float32 { return p.view.xOf(float64(f), p.span(), w) }

func (p *Page) frameAt(x, w float32) int { return p.view.frameAt(x, w, p.span()) }

// laneWidth is the width of the lanes (ruler width), 0 before layout.
func (p *Page) laneWidth() float32 {
	if p.tracks == nil {
		return 0
	}
	return p.tracks.ruler.Size().Width
}

// setZoom zooms around the lane position x (pixels), or around the playhead if it is
// visible (else the centre) when x < 0.
func (p *Page) setZoom(zoom float64, x float32) {
	span, fps := p.span(), p.s.FPS()
	w := p.laneWidth()
	frac := 0.5
	anchor := p.view.start + p.view.visible(span)/2
	if x >= 0 && w > 0 {
		frac = float64(x / w)
		anchor = p.view.start + frac*p.view.visible(span)
	} else if px := p.xOf(p.s.Playhead, w); w > 0 && px >= 0 && px <= w {
		frac = float64(px / w)
		anchor = float64(p.s.Playhead)
	}
	old := p.view
	p.view.zoomAround(zoom, anchor, frac, span, fps)
	p.syncZoomSlider()
	if p.view != old {
		p.redrawLanes()
	}
}

// scrollLanes pans the lanes by dx pixels.
func (p *Page) scrollLanes(dx float32) {
	w := p.laneWidth()
	if w <= 0 {
		return
	}
	old := p.view
	p.view.scrollBy(float64(dx/w)*p.view.visible(p.span()), p.span(), p.s.FPS())
	if p.view != old {
		p.redrawLanes()
	}
}

// wheel handles a scroll event over the lanes or the ruler: Ctrl zooms around the pointer,
// Shift or a horizontal wheel pans; it reports false for plain vertical scrolling.
func (p *Page) wheel(ev *fyne.ScrollEvent, x float32) bool {
	switch {
	case p.ctrlHeld():
		d := float64(ev.Scrolled.DY + ev.Scrolled.DX)
		p.setZoom(p.view.zoom*math.Pow(1.25, d/10), x)
	case p.shiftHeld():
		p.scrollLanes(-(ev.Scrolled.DY + ev.Scrolled.DX) * 3)
	case ev.Scrolled.DX != 0:
		p.scrollLanes(-ev.Scrolled.DX * 3)
	default:
		return false
	}
	return true
}

func (p *Page) syncZoomSlider() {
	if p.zoom == nil {
		return
	}
	v := zoomToSlider(p.view.zoom, maxZoom(p.span(), p.s.FPS()))
	if math.Abs(p.zoom.Value-v) > 1e-9 {
		p.zoom.Value = v // set directly: SetValue would fire OnChanged
		p.zoom.Refresh()
	}
}

// followPlayhead keeps the playhead on screen, paging in the play direction.
func (p *Page) followPlayhead() {
	if p.tracks == nil {
		return
	}
	if p.view.follow(p.s.Playhead, p.s.Playing, p.s.Rate, p.span(), p.s.FPS()) {
		p.redrawLanes()
	}
}

func (p *Page) redrawLanes() {
	if p.tracks != nil {
		p.tracks.redraw()
	}
}

// waveFor returns the clip's media and its cached waveform, starting a load if needed.
func (p *Page) waveFor(c model.Clip) (*waveEntry, *model.MediaItem) {
	m, ok := p.s.Project.MediaByID(c.MediaID)
	if !ok {
		return nil, nil
	}
	if e := p.peaks[m.ID]; e != nil {
		return e, m
	}
	e := &waveEntry{loading: true}
	p.peaks[m.ID] = e
	if p.waves == nil {
		e.loading, e.err = false, errors.New("keine Medienanbindung")
		return e, m
	}
	item, src := *m, p.waves
	buckets := min(max(int(item.Duration.Seconds()*100), 512), 40000)
	p.spawn(func() {
		peaks, err := src.Waveform(item, buckets)
		kit.Do(func() {
			e.peaks, e.err, e.loading = peaks, err, false
			if !p.closed && p.tracks != nil {
				p.tracks.refreshLanes()
			}
		})
	})
	return e, m
}

func (p *Page) editTrack(label, id string, fn func(t *model.Track)) {
	err := p.s.Edit(label, func(pr *model.Project) error {
		t, _ := pr.Track(id)
		if t == nil {
			return model.ErrNotFound
		}
		fn(t)
		return nil
	})
	if err != nil {
		p.showErr(fmt.Errorf("Spur lässt sich nicht ändern: %w", err))
	}
}

func (p *Page) track(id string) *model.Track {
	t, _ := p.s.Project.Track(id)
	if t == nil {
		return &model.Track{}
	}
	return t
}

func (p *Page) toggleMute(id string) {
	label := "Spur stummschalten"
	if p.track(id).Muted {
		label = "Stummschaltung aufheben"
	}
	p.editTrack(label, id, func(t *model.Track) { t.Muted = !t.Muted })
}

func (p *Page) toggleSolo(id string) {
	label := "Spur solo schalten"
	if p.track(id).Solo {
		label = "Solo aufheben"
	}
	p.editTrack(label, id, func(t *model.Track) { t.Solo = !t.Solo })
}

func (p *Page) toggleLock(id string) {
	label := "Spur sperren"
	if p.track(id).Locked {
		label = "Spur entsperren"
	}
	p.editTrack(label, id, func(t *model.Track) { t.Locked = !t.Locked })
}

func (p *Page) setTrackVolume(id string, db float64) {
	err := p.s.EditCoalesced("trackvol:"+id, "Spurlautstärke", func(pr *model.Project) error {
		t, _ := pr.Track(id)
		if t == nil {
			return model.ErrNotFound
		}
		t.Volume = DBToLinear(db)
		return nil
	})
	if err != nil {
		p.showErr(fmt.Errorf("Lautstärke lässt sich nicht setzen: %w", err))
	}
}

func (p *Page) addTrack() {
	var id string
	err := p.s.Edit("Audiospur hinzufügen", func(pr *model.Project) error {
		id = pr.AddTrack(model.TrackAudio).ID
		return nil
	})
	if err != nil {
		p.showErr(err)
		return
	}
	p.s.SelectTrack(id)
}

// renameTrack gives a track a new name as one undo step.
func (p *Page) renameTrack(id, name string) {
	if t, _ := p.s.Project.Track(id); t == nil || t.Name == name {
		return
	}
	p.editTrack("Spur umbenennen", id, func(t *model.Track) { t.Name = name })
}

// removeTrack deletes an empty audio track. Tracks with clips are kept: their clips would
// be lost without a word.
func (p *Page) removeTrack(id string) {
	t, _ := p.s.Project.Track(id)
	if t == nil {
		return
	}
	if len(t.Clips) > 0 {
		p.showErr(fmt.Errorf("Spur %s enthält noch Clips. Erst die Clips löschen oder verschieben, dann die Spur löschen.", t.Name))
		return
	}
	name := t.Name
	err := p.s.Edit("Spur "+name+" löschen", func(pr *model.Project) error { return pr.RemoveTrack(id) })
	switch {
	case errors.Is(err, model.ErrLocked):
		p.showErr(fmt.Errorf("Spur %s ist gesperrt. Im Spurkopf das Schloss lösen und erneut löschen.", name))
	case err != nil:
		p.showErr(fmt.Errorf("Spur lässt sich nicht löschen: %w", err))
	}
}
