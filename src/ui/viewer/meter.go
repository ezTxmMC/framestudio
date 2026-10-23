package viewer

import (
	"image/color"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/liveaudio"
	studiotheme "framestudio-proto/src/ui/theme"
)

// Meter scale and zones in dBFS.
const (
	meterFloor = -60.0
	meterAmber = -12.0
	meterRed   = -3.0
	// MeterHz is how often the meter polls liveaudio.Levels while playing.
	MeterHz = 30
	// peakHold is how long the peak marker stays before it falls.
	peakHold = time.Second
	// peakFall is how fast a released peak marker falls, in dB per second.
	peakFall = 24.0
)

var (
	meterGreen   = color.NRGBA{0x3D, 0xB0, 0x5C, 0xFF}
	meterAmberC  = color.NRGBA{0xE0, 0xA8, 0x30, 0xFF}
	meterRedC    = color.NRGBA{0xD8, 0x4A, 0x3C, 0xFF}
	meterTrough  = color.NRGBA{0x12, 0x13, 0x16, 0xFF}
	meterUnlitG  = color.NRGBA{0x3D, 0xB0, 0x5C, 0x22}
	meterUnlitA  = color.NRGBA{0xE0, 0xA8, 0x30, 0x22}
	meterUnlitR  = color.NRGBA{0xD8, 0x4A, 0x3C, 0x22}
	meterBarH    = float32(5)
	meterBarGap  = float32(3)
	meterWidth   = float32(96)
	meterLabelW  = float32(10)
	meterClipW   = float32(5)
	meterMinSize = fyne.NewSize(meterLabelW+meterWidth+meterClipW+4, 2*meterBarH+meterBarGap)
)

// dbPos maps a linear level to 0..1 on the meter scale.
func dbPos(lin float32) float32 {
	if lin <= 0 {
		return 0
	}
	db := 20 * math.Log10(float64(lin))
	return float32(math.Max(0, math.Min(1, (db-meterFloor)/-meterFloor)))
}

// Meter is a compact horizontal stereo master meter (L over R) with peak hold.
// It polls liveaudio.Levels at MeterHz only while the session plays.
type Meter struct {
	widget.BaseWidget
	s *session.Session

	// levels reads the master level on the UI thread (liveaudio.Levels by default).
	levels func(*session.Session) liveaudio.Level
	now    func() time.Time

	level   liveaudio.Level
	hold    [2]float32 // held peak position 0..1
	holdAt  [2]time.Time
	clipped bool

	unsub  func()
	closed bool
	hidden bool // the page showing the meter is not visible: no polling

	mu      sync.Mutex
	stop    chan struct{} // non-nil while the poll goroutine runs
	done    chan struct{}
	pending atomic.Bool // a poll is queued on the UI thread
}

// NewMeter creates a master meter for s. Call Close when done.
func NewMeter(s *session.Session) *Meter {
	m := &Meter{s: s, now: time.Now, levels: func(s *session.Session) liveaudio.Level {
		l, _ := liveaudio.Levels(s)
		return l
	}}
	m.ExtendBaseWidget(m)
	m.unsub = s.Subscribe(func(c session.Change) {
		if c&session.ChangePlayback != 0 {
			m.follow()
		}
	})
	m.follow()
	return m
}

// Running reports whether the poll ticker is active.
func (m *Meter) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stop != nil
}

// Close stops polling and unsubscribes; it waits for the poll goroutine to end.
func (m *Meter) Close() {
	if m.closed {
		return
	}
	m.closed = true
	m.unsub()
	m.halt()
}

// SetActive tells the meter whether its page is visible. A hidden meter does not poll,
// even while the session plays (playback started on another page); it resumes when
// shown again. UI thread only.
func (m *Meter) SetActive(on bool) {
	if m.hidden == !on {
		return
	}
	m.hidden = !on
	m.follow()
}

func (m *Meter) follow() {
	if m.closed {
		return
	}
	if m.s.Playing && !m.hidden {
		m.start()
		return
	}
	m.halt()
	m.reset()
}

func (m *Meter) start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stop != nil {
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	m.stop, m.done = stop, done
	go func() {
		defer close(done)
		t := time.NewTicker(time.Second / MeterHz)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			// One poll in flight at most: a busy UI thread skips ticks instead of
			// building a backlog.
			if !m.pending.CompareAndSwap(false, true) {
				continue
			}
			kit.Do(func() {
				m.pending.Store(false)
				select {
				case <-stop:
					return
				default:
				}
				m.poll()
			})
		}
	}()
}

// halt stops the poll goroutine and waits for it.
func (m *Meter) halt() {
	m.mu.Lock()
	stop, done := m.stop, m.done
	m.stop, m.done = nil, nil
	m.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
	m.pending.Store(false)
}

func (m *Meter) reset() {
	m.level = liveaudio.Level{}
	m.hold = [2]float32{}
	m.clipped = false
	m.Refresh()
}

// poll reads the current level and updates bars and peak hold (UI thread).
func (m *Meter) poll() {
	if m.closed {
		return
	}
	m.SetLevel(m.levels(m.s))
}

// SetLevel shows l and updates the peak hold markers (UI thread).
func (m *Meter) SetLevel(l liveaudio.Level) {
	now := m.now()
	for ch := 0; ch < 2; ch++ {
		p := dbPos(l.Peak[ch])
		switch {
		case p >= m.hold[ch]:
			m.hold[ch], m.holdAt[ch] = p, now
		case now.Sub(m.holdAt[ch]) > peakHold:
			fall := float32(now.Sub(m.holdAt[ch])-peakHold) / float32(time.Second) * float32(peakFall/-meterFloor)
			m.hold[ch] = max(p, m.hold[ch]-fall)
			m.holdAt[ch] = now.Add(-peakHold)
		}
	}
	m.level = l
	m.clipped = l.Clipped
	m.Refresh()
}

// Hold returns the held peak positions (0..1) for tests.
func (m *Meter) Hold() [2]float32 { return m.hold }

func (m *Meter) MinSize() fyne.Size { return meterMinSize }

func (m *Meter) CreateRenderer() fyne.WidgetRenderer {
	r := &meterRenderer{m: m}
	for ch := 0; ch < 2; ch++ {
		label := canvas.NewText([]string{"L", "R"}[ch], studiotheme.Faint)
		label.TextSize = 9
		label.TextStyle.Monospace = true
		r.labels[ch] = label
		r.trough[ch] = canvas.NewRectangle(meterTrough)
		r.unlit[ch] = [3]*canvas.Rectangle{canvas.NewRectangle(meterUnlitG), canvas.NewRectangle(meterUnlitA), canvas.NewRectangle(meterUnlitR)}
		r.lit[ch] = [3]*canvas.Rectangle{canvas.NewRectangle(meterGreen), canvas.NewRectangle(meterAmberC), canvas.NewRectangle(meterRedC)}
		r.peak[ch] = canvas.NewRectangle(studiotheme.Text)
	}
	r.clip = canvas.NewRectangle(meterTrough)
	r.clip.CornerRadius = 1
	r.objs = append(r.objs, r.clip)
	for ch := 0; ch < 2; ch++ {
		r.objs = append(r.objs, r.labels[ch], r.trough[ch])
		for z := 0; z < 3; z++ {
			r.objs = append(r.objs, r.unlit[ch][z])
		}
		for z := 0; z < 3; z++ {
			r.objs = append(r.objs, r.lit[ch][z])
		}
		r.objs = append(r.objs, r.peak[ch])
	}
	return r
}

type meterRenderer struct {
	m      *Meter
	labels [2]*canvas.Text
	trough [2]*canvas.Rectangle
	unlit  [2][3]*canvas.Rectangle
	lit    [2][3]*canvas.Rectangle
	peak   [2]*canvas.Rectangle
	clip   *canvas.Rectangle
	objs   []fyne.CanvasObject
}

// zones are the meter segments as fractions of the scale.
var zones = [4]float32{0, float32((meterAmber - meterFloor) / -meterFloor), float32((meterRed - meterFloor) / -meterFloor), 1}

func (r *meterRenderer) Layout(size fyne.Size) {
	m := r.m
	w := max(10, size.Width-meterLabelW-meterClipW-4)
	total := 2*meterBarH + meterBarGap
	top := (size.Height - total) / 2
	x0 := meterLabelW
	for ch := 0; ch < 2; ch++ {
		y := top + float32(ch)*(meterBarH+meterBarGap)
		r.labels[ch].Move(fyne.NewPos(0, y-3))
		r.labels[ch].Resize(fyne.NewSize(meterLabelW, 10))
		r.trough[ch].Move(fyne.NewPos(x0-1, y-1))
		r.trough[ch].Resize(fyne.NewSize(w+2, meterBarH+2))
		lvl := dbPos(m.level.Peak[ch])
		for z := 0; z < 3; z++ {
			a, b := zones[z]*w, zones[z+1]*w
			r.unlit[ch][z].Move(fyne.NewPos(x0+a, y))
			r.unlit[ch][z].Resize(fyne.NewSize(b-a-1, meterBarH))
			lit := min(b, lvl*w) - a
			if lit <= 0 {
				r.lit[ch][z].Hide()
				continue
			}
			r.lit[ch][z].Show()
			r.lit[ch][z].Move(fyne.NewPos(x0+a, y))
			r.lit[ch][z].Resize(fyne.NewSize(min(lit, b-a-1), meterBarH))
		}
		if h := m.hold[ch]; h > 0 {
			r.peak[ch].Show()
			r.peak[ch].FillColor = zoneColor(h)
			r.peak[ch].Move(fyne.NewPos(x0+max(0, h*w-2), y))
			r.peak[ch].Resize(fyne.NewSize(2, meterBarH))
		} else {
			r.peak[ch].Hide()
		}
	}
	r.clip.FillColor = meterTrough
	if m.clipped {
		r.clip.FillColor = meterRedC
	}
	r.clip.Move(fyne.NewPos(x0+w+3, top-1))
	r.clip.Resize(fyne.NewSize(meterClipW, total+2))
}

func zoneColor(p float32) color.Color {
	switch {
	case p >= zones[2]:
		return meterRedC
	case p >= zones[1]:
		return meterAmberC
	}
	return meterGreen
}

func (r *meterRenderer) MinSize() fyne.Size { return meterMinSize }
func (r *meterRenderer) Refresh() {
	r.Layout(r.m.Size())
	for _, o := range r.objs {
		o.Refresh()
	}
}
func (r *meterRenderer) Destroy()                     {}
func (r *meterRenderer) Objects() []fyne.CanvasObject { return r.objs }
