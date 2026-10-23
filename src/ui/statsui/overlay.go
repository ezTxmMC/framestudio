package statsui

import (
	"image/color"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
)

var (
	panelFill = color.NRGBA{0x00, 0x00, 0x00, 0x99}
	lineColor = color.NRGBA{0xE4, 0xE6, 0xE2, 0xFF}
	amber     = color.NRGBA{0xE0, 0xA1, 0x2B, 0xFF}
	amberText = color.NRGBA{0x1A, 0x14, 0x06, 0xFF}
)

const (
	textSize = 11
	pad      = 8
	// refresh is the poll rate of the statistics (2 Hz); nothing is posted to the UI
	// thread unless the displayed text or a chip changed.
	refresh = 500 * time.Millisecond
)

// Overlay is the statistics panel (top left) and the warning chips (top right) drawn
// over the viewer. It does not intercept mouse input.
type Overlay struct {
	widget.BaseWidget

	src Source
	now func() time.Time

	bg      *canvas.Rectangle
	lines   [3]*canvas.Text
	chipsUI [2]*chip

	mu    sync.Mutex
	state chips
	last  [3]string
	show  bool // statistics panel visible
	over  bool // chips as last posted to the UI thread
	kf    bool
	stop  chan struct{}
}

// chip is one amber label.
type chip struct {
	bg  *canvas.Rectangle
	txt *canvas.Text
}

func newChip(text string) *chip {
	c := &chip{bg: canvas.NewRectangle(amber), txt: kit.MonoText(text, amberText, textSize)}
	c.bg.CornerRadius = 4
	c.bg.Hide()
	c.txt.Hide()
	return c
}

func (c *chip) set(on bool) {
	if on == c.bg.Visible() {
		return
	}
	if on {
		c.bg.Show()
		c.txt.Show()
	} else {
		c.bg.Hide()
		c.txt.Hide()
	}
}

// New creates the overlay. The statistics panel follows settings.Playback.Stats (see
// FollowSettings); the chips show regardless. A nil source shows nothing.
func New(src Source) *Overlay {
	o := &Overlay{src: src, now: time.Now, bg: canvas.NewRectangle(panelFill),
		stop: make(chan struct{})}
	o.bg.CornerRadius = 4
	for i := range o.lines {
		o.lines[i] = kit.MonoText("", lineColor, textSize)
	}
	o.chipsUI = [2]*chip{newChip(ChipOverload), newChip(ChipKeyframes)}
	o.bg.Hide()
	for _, l := range o.lines {
		l.Hide()
	}
	o.ExtendBaseWidget(o)
	return o
}

// FollowSettings shows and hides the panel with settings.Playback.Stats. Call the
// returned func to stop following.
func (o *Overlay) FollowSettings() func() {
	apply := func(s settings.Settings) { o.setShown(s.Playback.Stats) }
	apply(settings.Get())
	return settings.Subscribe(apply)
}

func (o *Overlay) setShown(on bool) {
	o.mu.Lock()
	o.show = on
	o.mu.Unlock()
	if on == o.bg.Visible() {
		return
	}
	if on {
		o.bg.Show()
		for _, l := range o.lines {
			l.Show()
		}
		o.tick() // fill at once instead of waiting for the next poll
	} else {
		o.bg.Hide()
		for _, l := range o.lines {
			l.Hide()
		}
	}
	o.Refresh()
}

// Start begins polling the source at 2 Hz in the background; Close ends it.
func (o *Overlay) Start() {
	go func() {
		t := time.NewTicker(refresh)
		defer t.Stop()
		for {
			select {
			case <-o.stop:
				return
			case <-t.C:
				o.poll()
			}
		}
	}()
}

// Close stops polling.
func (o *Overlay) Close() {
	select {
	case <-o.stop:
	default:
		close(o.stop)
	}
}

// poll runs off the UI thread: it reads the source, formats and posts only on a change.
func (o *Overlay) poll() {
	if o.src == nil {
		return
	}
	snap := o.src.Snapshot()
	o.mu.Lock()
	over, kf := o.state.update(o.now(), snap)
	lines := o.last
	shown := o.show
	if shown {
		lines = Lines(snap)
	}
	changed := lines != o.last || over != o.over || kf != o.kf
	o.last, o.over, o.kf = lines, over, kf
	o.mu.Unlock()
	if !changed {
		return
	}
	kit.Do(func() { o.apply(lines, over, kf) })
}

// tick polls once on the calling goroutine (UI thread) when the panel is switched on.
func (o *Overlay) tick() {
	if o.src == nil {
		return
	}
	snap := o.src.Snapshot()
	o.mu.Lock()
	over, kf := o.state.update(o.now(), snap)
	lines := Lines(snap)
	o.last, o.over, o.kf = lines, over, kf
	o.mu.Unlock()
	o.apply(lines, over, kf)
}

// apply updates the canvas objects (UI thread).
func (o *Overlay) apply(lines [3]string, over, kf bool) {
	for i, l := range o.lines {
		if l.Text != lines[i] {
			l.Text = lines[i]
			l.Refresh()
		}
	}
	o.chipsUI[0].set(over)
	o.chipsUI[1].set(kf)
	o.Refresh()
}

// CreateRenderer implements fyne.Widget.
func (o *Overlay) CreateRenderer() fyne.WidgetRenderer {
	objs := []fyne.CanvasObject{o.bg}
	for _, l := range o.lines {
		objs = append(objs, l)
	}
	for _, c := range o.chipsUI {
		objs = append(objs, c.bg, c.txt)
	}
	return &overlayRenderer{o: o, objs: objs}
}

type overlayRenderer struct {
	o    *Overlay
	objs []fyne.CanvasObject
}

func (r *overlayRenderer) Destroy()                     {}
func (r *overlayRenderer) MinSize() fyne.Size           { return fyne.NewSize(0, 0) }
func (r *overlayRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *overlayRenderer) Refresh()                     { r.Layout(r.o.Size()) }

func (r *overlayRenderer) Layout(size fyne.Size) {
	o := r.o
	// the panel keeps the width of a typical longest line so it does not jitter
	l0 := o.lines[0]
	maxW := fyne.MeasureText(widest, l0.TextSize, l0.TextStyle).Width
	for _, l := range o.lines {
		maxW = max(maxW, fyne.MeasureText(l.Text, l.TextSize, l.TextStyle).Width)
	}
	y := float32(pad)
	lineH := o.lines[0].TextSize * 1.3
	o.bg.Move(fyne.NewPos(pad, pad))
	o.bg.Resize(fyne.NewSize(maxW+2*pad, 3*lineH+2*pad))
	y = pad + pad
	for _, l := range o.lines {
		l.Move(fyne.NewPos(2*pad, y))
		l.Resize(fyne.NewSize(maxW, lineH))
		y += lineH
	}
	// chips stack at the top right
	cy := float32(pad)
	for _, c := range o.chipsUI {
		w := fyne.MeasureText(c.txt.Text, c.txt.TextSize, c.txt.TextStyle).Width + 2*pad
		h := c.txt.TextSize*1.3 + 8
		c.bg.Move(fyne.NewPos(size.Width-w-pad, cy))
		c.bg.Resize(fyne.NewSize(w, h))
		c.txt.Move(fyne.NewPos(size.Width-w-pad+pad, cy+4))
		c.txt.Resize(fyne.NewSize(w-2*pad, c.txt.TextSize*1.3))
		if c.bg.Visible() {
			cy += h + 4
		}
	}
}

const widest = "Qualität: Viertel (auto)  1920x1080  Render 88.8 ms"

// Wrap puts a statistics overlay over obj (a viewer) and returns the stacked object and
// the overlay. The overlay follows settings.Playback.Stats and polls src at 2 Hz until
// Close.
func Wrap(obj fyne.CanvasObject, src Source) (fyne.CanvasObject, *Overlay) {
	o := New(src)
	st := container.NewStack(obj, o)
	o.Start()
	return st, o
}
