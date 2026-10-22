// Package audio is the "audio" workspace page.
//
// OWNER: audio page agent. New(ctx) is the contract; do not change its signature.
//
// The page follows Resolve's Fairlight layout: audio tracks with waveforms on top, the
// selected clip and a channel-strip mixer below. "Vorhören" mixes the timeline audio
// through export and plays it with an external player, since Fyne has no audio output.
package audio

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
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

	prev    *previewer
	tracks  *trackView
	mixer   *mixer
	clip    *clipPanel
	play    *widget.Button
	stop    *widget.Button
	status  *canvas.Text
	content fyne.CanvasObject
	unsub   func()
	active  bool
	stale   bool
	closed  bool
}

func New(ctx *page.Context) page.Page {
	p := &Page{ctx: ctx, s: ctx.Session, active: true, peaks: map[string]*waveEntry{}, spawn: func(f func()) { go f() }}
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
	return p
}

func (p *Page) Content() fyne.CanvasObject {
	if p.content != nil {
		return p.content
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
	tools := container.NewHBox(container.NewCenter(p.status), fixedSize(6, 0), p.play, p.stop)

	add := widget.NewButtonWithIcon("Spur hinzufügen", theme.ContentAddIcon(), p.addTrack)
	add.Importance = widget.LowImportance

	top := kit.Panel("Spuren", tools, p.tracks.obj)
	bottom := container.NewHSplit(kit.Panel("Clip", nil, p.clip.obj), kit.Panel("Mixer", add, p.mixer.obj))
	bottom.Offset = 0.3
	split := container.NewVSplit(top, bottom)
	split.Offset = 0.55
	p.content = container.NewStack(canvas.NewRectangle(studiotheme.Gap),
		container.New(layout.NewCustomPaddedLayout(4, 4, 4, 4), split))

	p.unsub = p.s.Subscribe(p.changed)
	p.refresh()
	return p.content
}

func (p *Page) Activate() {
	p.active = true
	if p.stale && p.content != nil {
		p.refresh()
	}
}

// Deactivate stops a running preview: its Stopp button is not reachable from other pages.
func (p *Page) Deactivate() {
	p.active = false
	p.prev.Stop()
}

// TypedKey: Space toggles the preview, Left/Right step one frame, Home/End jump.
func (p *Page) TypedKey(k *fyne.KeyEvent) bool {
	switch k.Name {
	case fyne.KeySpace:
		if p.prev.Busy() {
			p.prev.Stop()
		} else {
			p.prev.Start()
		}
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

func (p *Page) Close() {
	p.closed = true
	p.prev.Stop()
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
	if c&(session.ChangeProject|session.ChangeSelection) != 0 {
		p.refresh()
		return
	}
	if c&session.ChangePlayhead != 0 {
		p.tracks.updatePlayhead()
		p.updateTransport()
	}
}

func (p *Page) refresh() {
	p.stale = false
	p.tracks.update()
	p.mixer.update()
	p.clip.update()
	p.updateTransport()
}

func (p *Page) updateTransport() {
	if p.content == nil {
		return
	}
	busy := p.prev.Busy()
	text := p.prev.status
	if !busy {
		if p.s.HasRange() {
			text = "In/Out-Bereich, max. 30 s"
		} else {
			text = "Ab Abspielkopf, max. 30 s"
		}
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

// span is the number of frames shown across the lanes: the program plus some headroom.
func (p *Page) span() int {
	fps := max(p.s.FPS(), 1)
	d := max(p.s.Project.Duration(), 10*fps)
	return d + d/16
}

func (p *Page) xOf(f int, w float32) float32 { return float32(f) / float32(p.span()) * w }

func (p *Page) frameAt(x, w float32) int {
	if w <= 0 || x <= 0 {
		return 0
	}
	return int(x / w * float32(p.span()))
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
