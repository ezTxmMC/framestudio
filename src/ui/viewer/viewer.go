// Package viewer is the program monitor: an async-rendered 16:9 (project aspect) view of
// the frame under the playhead, plus a transport bar.
//
// OWNER: edit page agent. Exported signatures in this file are a contract; do not change them.
package viewer

import (
	"image"
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// Preview size limit while playing in automatic quality without a configured maximum
// height; paused frames render at full widget resolution.
const playW, playH = 1280, 720

// Preview size limit while shuttling (J/K/L at other than 1×).
const shuttleW, shuttleH = 854, 480

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

	// shared with the render goroutine
	mu      sync.Mutex
	pending *request
	wake    chan struct{}
	quit    chan struct{}
	running bool
	// ready is the newest rendered frame not yet shown; while one waits for the UI
	// thread, newer frames replace it instead of queueing (no backlog when shuttling).
	ready *image.NRGBA
}

type request struct {
	p    *model.Project
	f    int
	w, h int
	opts render.Options
	src  func(p *model.Project, f, w, h int, o render.Options) *image.NRGBA
	post func(func())
}

func (q request) same(o request) bool {
	return q.p == o.p && q.f == o.f && q.w == o.w && q.h == o.h && q.opts.SkipGrade == o.opts.SkipGrade
}

func New(s *session.Session, r *render.Renderer) *Viewer {
	v := &Viewer{s: s, r: r, active: true, post: kit.Do,
		wake: make(chan struct{}, 1), quit: make(chan struct{})}
	v.img = canvas.NewImageFromImage(nil)
	v.img.FillMode = canvas.ImageFillContain
	v.img.ScaleMode = canvas.ImageScaleSmooth
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
	v.pic = container.NewStack(v.fill, v.img, v.safe.obj,
		container.NewBorder(container.New(layout.NewCustomPaddedLayout(8, 0, 8, 0), v.badge), nil, nil, nil))
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

// Image returns the frame currently shown, or nil before the first render.
func (v *Viewer) Image() *image.NRGBA {
	img, _ := v.img.Image.(*image.NRGBA)
	return img
}

// Close stops rendering and unsubscribes.
func (v *Viewer) Close() {
	if v.closed {
		return
	}
	v.closed = true
	v.unsub()
	v.unsubSetting()
	close(v.quit)
}

func (v *Viewer) changed(c session.Change) {
	if c&session.ChangeProject != 0 {
		v.snap = v.s.Project.Clone()
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
}

func aspect(p *model.Project) float32 {
	if p.Settings.Width <= 0 || p.Settings.Height <= 0 {
		return 16.0 / 9
	}
	return float32(p.Settings.Width) / float32(p.Settings.Height)
}

// renderSize is the pixel size of the picture area, limited while playing and never
// above the project resolution.
func (v *Viewer) renderSize() (int, int) {
	scale := float32(1)
	if c := fyne.CurrentApp().Driver().CanvasForObject(v); c != nil {
		scale = c.Scale()
	}
	size := v.pic.Size()
	w, h := float64(size.Width*scale), float64(size.Height*scale)
	if w < 1 || h < 1 {
		return 0, 0
	}
	limit := func(maxW, maxH float64) {
		k := min(1, maxW/w, maxH/h)
		w, h = w*k, h*k
	}
	// the project resolution is the most a preview needs
	limit(float64(v.snap.Settings.Width), float64(v.snap.Settings.Height))
	switch q := v.Quality(); q {
	case QualityHalf, QualityQuarter:
		k := 0.5
		if q == QualityQuarter {
			k = 0.25
		}
		w, h = w*k, h*k
	}
	if v.s.Playing && v.Quality() != QualityFull {
		if v.maxH > 0 {
			limit(w, float64(v.maxH))
		} else {
			limit(playW, playH)
		}
		if v.s.Rate != 1 {
			// Shuttling skips frames anyway; smaller frames keep up with fast and
			// reverse playback (decoding backwards is expensive).
			limit(shuttleW, shuttleH)
		}
	}
	return max(1, int(w+0.5)), max(1, int(h+0.5))
}

// requestRender queues the current frame; a newer request replaces one not yet started.
func (v *Viewer) requestRender() {
	if v.closed || !v.active {
		return
	}
	w, h := v.renderSize()
	if w == 0 {
		return
	}
	src := v.Source
	if src == nil && v.r != nil {
		src = v.r.FrameWith
	}
	if src == nil {
		return
	}
	opts := v.opts
	opts.Rate = 0
	if v.s.Playing {
		// Shuttle rates let the media cache decode ahead (reverse) or skip frames (fast).
		opts.Rate = v.s.Rate
	}
	req := request{p: v.snap, f: v.s.Playhead, w: w, h: h, opts: opts, src: src, post: v.post}
	if req.same(v.last) {
		return
	}
	v.last = req
	v.mu.Lock()
	v.pending = &req
	if !v.running {
		v.running = true
		go v.run()
	}
	v.mu.Unlock()
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

func (v *Viewer) run() {
	for {
		select {
		case <-v.quit:
			return
		case <-v.wake:
		}
		v.mu.Lock()
		req := v.pending
		v.pending = nil
		v.mu.Unlock()
		if req == nil {
			continue
		}
		img := req.src(req.p, req.f, req.w, req.h, req.opts)
		if img == nil {
			continue
		}
		select {
		case <-v.quit:
			return
		default:
		}
		v.mu.Lock()
		queued := v.ready != nil
		v.ready = img
		v.mu.Unlock()
		if !queued {
			req.post(v.showReady)
		}
	}
}

func (v *Viewer) showReady() {
	v.mu.Lock()
	img := v.ready
	v.ready = nil
	v.mu.Unlock()
	if img != nil {
		v.show(img)
	}
}

func (v *Viewer) show(img *image.NRGBA) {
	if v.closed {
		return
	}
	v.img.Image = img
	v.img.Refresh()
	if v.OnFrame != nil {
		v.OnFrame(img)
	}
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
