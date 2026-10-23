// Package viewer is the program monitor: an async-rendered 16:9 (project aspect) view of
// the frame under the playhead, plus a transport bar.
//
// OWNER: edit page agent. Exported signatures in this file are a contract; do not change them.
package viewer

import (
	"context"
	"errors"
	"image"
	"image/color"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/preview"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

type Viewer struct {
	widget.BaseWidget
	s *session.Session
	r *render.Renderer

	// OnFrame is called on the UI thread with every newly rendered preview frame
	// (the colour page uses it for scopes). The image must not be modified.
	OnFrame func(img *image.NRGBA)

	// Source, when set, renders frames instead of the Renderer. It runs on the render
	// goroutine and gets a project snapshot nobody else mutates.
	Source func(p *model.Project, f, w, h int, o render.Options) *image.NRGBA

	img    *canvas.Image
	frame  *fyne.Container // aspect box holding pic
	pic    *fyne.Container
	fill   *canvas.Rectangle // project background behind the frame
	badge  *fyne.Container
	snap   *model.Project
	ratio  float32
	opts   render.Options
	active bool
	closed bool
	last   request
	unsub  func()
	post   func(func())

	// preview quality (settings Playback.PreviewQuality / MaxPreviewH, see quality.go)
	quality      string // QualityAuto, QualityFull, QualityHalf or QualityQuarter
	override     string // SetQuality; "" follows the settings
	maxH         int
	safe         *safeArea
	unsubSetting func()

	// The picture pipeline (see package preview). The render goroutine lives in sched;
	// the UI thread only swaps the newest ready frame into img.
	sched    *preview.Scheduler
	cache    *preview.FrameCache
	gov      *preview.LoadGovernor
	rev      uint64
	cur      *preview.Frame // frame displayed by img; released when replaced
	notified atomic.Bool    // a showReady post is in flight (coalesces notifications)
	monitor  atomic.Bool    // the playback monitor goroutine runs
	playingA atomic.Bool    // mirrors s.Playing for the monitor goroutine
	statsOn  atomic.Bool    // mirrors overlay.shown
	chipOn   atomic.Bool    // mirrors the visibility of the overload chip
	govern   bool
	overlay  *statsOverlay
	chipTill time.Time
}

// request identifies the last request handed to the scheduler, to skip duplicates.
type request struct {
	p       *model.Project
	rev     uint64
	f       int
	w, h    int
	skip    bool
	pin     string
	maxH    int
	playing bool
	rate    float64
}

func New(s *session.Session, r *render.Renderer) *Viewer {
	v := &Viewer{s: s, r: r, active: true, post: kit.Do, govern: true, rev: 1}
	v.img = canvas.NewImageFromImage(nil)
	v.img.FillMode = canvas.ImageFillContain
	// The frame is rendered at (or below) the widget's pixel size, so fyne must never
	// scale it on the CPU: ImageScaleFastest leaves any stretching to the GPU, and an
	// *image.RGBA goes to the texture without conversion.
	v.img.ScaleMode = canvas.ImageScaleFastest
	label := canvas.NewText("Vorher", studiotheme.Text)
	label.TextSize = 11
	chip := canvas.NewRectangle(color.NRGBA{A: 0xA0})
	chip.CornerRadius = 3
	v.badge = container.NewHBox(container.NewStack(chip,
		container.New(layout.NewCustomPaddedLayout(2, 2, 7, 7), label)))
	v.badge.Hide()
	v.snap = s.Project.Clone()
	v.ratio = aspect(v.snap)
	v.fill = canvas.NewRectangle(v.snap.Settings.Background.NRGBA())
	v.safe = newSafeArea()
	v.overlay = newStatsOverlay()
	v.pic = container.NewStack(v.fill, v.img, v.safe.obj,
		container.NewBorder(container.New(layout.NewCustomPaddedLayout(8, 0, 8, 0), v.badge), nil, nil, nil),
		v.overlay.obj)
	v.cache = preview.NewFrameCache(min(preview.DefaultCacheBytes()*2/3, 1<<30))
	v.gov = preview.NewLoadGovernor()
	v.sched = preview.New(preview.Config{Cache: v.cache, Governor: v.gov, PostProcess: v.prepare}, viewerRender{v})
	v.sched.OnReady(v.notify)
	v.frame = container.New(kit.AspectLayout{Ratio: v.ratio}, v.pic)
	v.unsub = s.Subscribe(v.changed)
	v.applySettings(settings.Get())
	v.unsubSetting = settings.Subscribe(v.applySettings)
	v.ExtendBaseWidget(v)
	return v
}

// SetDeliver replaces how rendered frames reach the UI thread (default kit.Do).
// Tests pass a queue they drain on their own goroutine.
func (v *Viewer) SetDeliver(post func(func())) { v.post = post }

// SetActive pauses rendering while the hosting page is hidden; reactivating renders
// the current frame.
func (v *Viewer) SetActive(on bool) {
	v.active = on
	if on {
		v.requestRender()
	}
}

// Active reports whether the viewer renders (see SetActive).
func (v *Viewer) Active() bool { return v.active }

// SetCompare renders the ungraded frame when on (colour page before/after).
func (v *Viewer) SetCompare(on bool) {
	v.opts.SkipGrade = on
	if on {
		v.badge.Show()
	} else {
		v.badge.Hide()
	}
	v.pic.Refresh() // re-layout for the badge
	v.requestRender()
}

// Image returns the frame currently shown as a straight-alpha image, or nil before the
// first render. It converts on demand; the display path never does.
func (v *Viewer) Image() *image.NRGBA {
	if v.cur == nil {
		return nil
	}
	return v.cur.NRGBA()
}

// ImageRGBA returns the displayed frame exactly as handed to fyne (premultiplied).
func (v *Viewer) ImageRGBA() *image.RGBA {
	if v.cur == nil {
		return nil
	}
	return v.cur.Img
}

// ShownPos is the timeline frame of the displayed picture (-1 before the first).
func (v *Viewer) ShownPos() int {
	if v.cur == nil {
		return -1
	}
	return v.cur.Pos
}

// QualityChanges counts automatic quality steps so far.
func (v *Viewer) QualityChanges() int { return v.sched.QualityChanges() }

// Stats returns the playback statistics collector.
func (v *Viewer) Stats() *preview.Stats { return v.sched.Stats() }

// Cache returns the rendered-frame cache (for the timeline's cached-range bar).
func (v *Viewer) Cache() *preview.FrameCache { return v.cache }

// SetGovernor enables or disables the UI-latency governor (on by default).
func (v *Viewer) SetGovernor(on bool) { v.govern = on }

// Close stops rendering and unsubscribes.
func (v *Viewer) Close() {
	if v.closed {
		return
	}
	v.closed = true
	v.unsub()
	v.unsubSetting()
	v.sched.Close()
	v.cache.Clear()
	v.cur.Release()
	v.cur = nil
}

func (v *Viewer) changed(c session.Change) {
	if c&session.ChangeProject != 0 {
		v.snap = v.s.Project.Clone()
		v.rev++
		if r := aspect(v.snap); r != v.ratio {
			v.ratio = r
			v.frame.Layout = kit.AspectLayout{Ratio: r}
			v.frame.Refresh()
		}
		if bg := v.snap.Settings.Background.NRGBA(); v.fill.FillColor != bg {
			v.fill.FillColor = bg
			v.fill.Refresh()
		}
	}
	if c&(session.ChangeProject|session.ChangePlayhead|session.ChangePlayback) != 0 {
		v.requestRender()
	}
	if c&session.ChangePlayback != 0 {
		v.playingA.Store(v.s.Playing)
		if v.s.Playing {
			v.startMonitor()
		}
	}
}

func aspect(p *model.Project) float32 {
	if p.Settings.Width <= 0 || p.Settings.Height <= 0 {
		return 16.0 / 9
	}
	return float32(p.Settings.Width) / float32(p.Settings.Height)
}

// target is the pixel size of the picture area as fyne's painter will size its texture,
// never above the project resolution (a preview needs no more).
func (v *Viewer) target() preview.Target {
	scale := float32(1)
	if c := fyne.CurrentApp().Driver().CanvasForObject(v); c != nil {
		scale = c.Scale()
	}
	size := v.pic.Size()
	w, h := preview.TextureSize(size.Width, size.Height, scale)
	if w < 1 || h < 1 {
		return preview.Target{}
	}
	if pw, ph := v.snap.Settings.Width, v.snap.Settings.Height; pw > 0 && ph > 0 && (w > pw || h > ph) {
		k := min(float64(pw)/float64(w), float64(ph)/float64(h))
		w, h = max(1, int(float64(w)*k+0.5)), max(1, int(float64(h)*k+0.5))
	}
	return preview.Target{W: w, H: h}
}

// renderSize is the pixel size the next frame is rendered at.
func (v *Viewer) renderSize() (int, int) {
	t := v.target()
	if t.Empty() {
		return 0, 0
	}
	v.sched.SetTarget(t)
	if v.s.Playing {
		return v.sched.PlanSize(preview.ReqPlay, v.s.Rate)
	}
	return v.sched.PlanSize(preview.ReqStill, 1)
}

// requestRender hands the current position to the scheduler; a newer request replaces
// one not yet started. It costs a few comparisons and never blocks.
func (v *Viewer) requestRender() {
	if v.closed || !v.active {
		return
	}
	t := v.target()
	if t.Empty() || (v.Source == nil && v.r == nil) {
		return
	}
	req := request{p: v.snap, rev: v.rev, f: v.s.Playhead, w: t.W, h: t.H, skip: v.opts.SkipGrade,
		pin: v.Quality(), maxH: v.maxH, playing: v.s.Playing, rate: v.s.Rate}
	if req == v.last {
		return
	}
	v.last = req
	v.sched.SetTarget(t)
	v.sched.SetProject(v.snap, v.rev)
	v.sched.SetCompare(req.skip)
	if req.pin == QualityFull {
		v.sched.SetMaxH(0) // full always means the whole widget
	} else {
		v.sched.SetMaxH(v.maxH)
	}
	v.sched.SetQualityPin(req.pin)
	if v.s.Playing {
		v.sched.SetSpeed(v.s.Rate)
		v.sched.Request(v.s.Playhead, preview.ReqPlay)
	} else {
		v.sched.SetSpeed(1)
		v.sched.Request(v.s.Playhead, preview.ReqScrub)
	}
}

// viewerRender adapts the viewer's render source (the Renderer, or a test Source) to
// preview.Renderer.
type viewerRender struct{ v *Viewer }

func (r viewerRender) RenderInto(ctx context.Context, dst *image.RGBA, p *model.Project, frame int, o render.Options) (render.Info, error) {
	v := r.v
	if src := v.Source; src != nil {
		img := src(p, frame, dst.Rect.Dx(), dst.Rect.Dy(), o)
		if img == nil {
			return render.Info{}, errors.New("no frame")
		}
		opaque := render.CopyNRGBA(dst, img)
		return render.Info{Opaque: opaque}, ctx.Err()
	}
	if v.r == nil {
		return render.Info{}, errors.New("no renderer")
	}
	return v.r.RenderInto(ctx, dst, p, frame, o)
}

// prepare runs on the scheduler goroutine for every frame before it is published.
func (v *Viewer) prepare(f *preview.Frame) {
	if v.OnFrame != nil {
		f.NRGBA() // scopes want straight alpha; convert here, not on the UI thread
	}
}

// notify is called by the scheduler when the ready slot got a frame. At most one UI
// post is in flight; the post takes whatever is newest when it runs.
func (v *Viewer) notify() {
	if v.notified.CompareAndSwap(false, true) {
		v.post(v.showReady)
	}
}

func (v *Viewer) showReady() {
	v.notified.Store(false)
	if f := v.sched.Take(); f != nil {
		v.show(f)
	}
}

// show is the whole UI-thread cost of a preview frame: swap the image pointer and let
// fyne upload it.
func (v *Viewer) show(f *preview.Frame) {
	if v.closed {
		f.Release()
		return
	}
	prev := v.cur
	v.cur = f
	v.img.Image = f.Img
	v.img.Refresh()
	v.sched.Stats().Shown(time.Now())
	if v.OnFrame != nil {
		v.OnFrame(f.NRGBA())
	}
	prev.Release()
}

func (v *Viewer) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(studiotheme.MonitorOut)
	return &viewerRenderer{v: v, bg: bg}
}

type viewerRenderer struct {
	v  *Viewer
	bg *canvas.Rectangle
}

func (r *viewerRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.v.frame.Resize(size)
	r.v.requestRender()
}

func (r *viewerRenderer) MinSize() fyne.Size { return fyne.NewSize(240, 135) }
func (r *viewerRenderer) Refresh()           { r.bg.Refresh(); r.v.frame.Refresh() }
func (r *viewerRenderer) Destroy()           {}
func (r *viewerRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.v.frame}
}

// Transport returns the bar under the monitor: timecode, transport buttons, loop, duration.
func Transport(s *session.Session) fyne.CanvasObject { return NewTransport(s) }
