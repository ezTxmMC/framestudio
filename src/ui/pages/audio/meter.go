package audio

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/liveaudio"
	studiotheme "framestudio-proto/src/ui/theme"
)

// Meter scale and ballistics.
const (
	meterFloor = -70.0 // dBFS shown as the bottom of the meter
	meterWarn  = -12.0 // green up to here
	meterHot   = -3.0  // amber up to here, red above

	meterFall = 24.0                    // bar release in dB per second
	peakHold  = 1500 * time.Millisecond // peak line holds this long
	peakFall  = 20.0                    // then falls in dB per second
)

// meterTicks are the labelled marks next to every meter.
var meterTicks = []float64{0, -6, -12, -24, -48}

// Meter colours: classic console zones. The red is kept apart from the Tally red.
var (
	meterGreen = color.NRGBA{0x3C, 0xB8, 0x64, 0xFF}
	meterAmber = color.NRGBA{0xE0, 0xA8, 0x30, 0xFF}
	meterRed   = color.NRGBA{0xF0, 0x52, 0x3A, 0xFF}
	meterBack  = color.NRGBA{0x12, 0x13, 0x16, 0xFF}
	clipOff    = color.NRGBA{0x3A, 0x22, 0x22, 0xFF}
)

// ampToDB converts a linear sample amplitude to dBFS, meterFloor for silence.
func ampToDB(a float32) float64 {
	if a <= 0 {
		return meterFloor
	}
	return math.Max(meterFloor, 20*math.Log10(float64(a)))
}

// meterFrac maps dBFS to the meter height 0..1 with the IEC 60268-18 scale, which gives
// the top 20 dB half of the meter.
func meterFrac(db float64) float64 {
	var p float64
	switch {
	case db < -70:
		p = 0
	case db < -60:
		p = (db + 70) * 0.25
	case db < -50:
		p = (db+60)*0.5 + 2.5
	case db < -40:
		p = (db+50)*0.75 + 7.5
	case db < -30:
		p = (db+40)*1.5 + 15
	case db < -20:
		p = (db+30)*2 + 30
	case db < 0:
		p = (db+20)*2.5 + 50
	default:
		p = 100
	}
	return p / 100
}

// zoneColor is the meter colour at level db.
func zoneColor(db float64) color.NRGBA {
	switch {
	case db > meterHot:
		return meterRed
	case db > meterWarn:
		return meterAmber
	}
	return meterGreen
}

// ballistics is the displayed state of one meter channel: the bar follows rises at once
// and falls at meterFall; the peak line holds, then falls; clip latches until reset.
type ballistics struct {
	level, peak float64 // dBFS
	hold        time.Duration
	clip        bool
}

func newBallistics() ballistics { return ballistics{level: meterFloor, peak: meterFloor} }

// step feeds a new input level (dBFS) after dt.
func (b *ballistics) step(in float64, clipped bool, dt time.Duration) {
	sec := dt.Seconds()
	in = math.Max(in, meterFloor)
	if in >= b.level {
		b.level = in
	} else {
		b.level = math.Max(in, b.level-meterFall*sec)
	}
	switch {
	case in >= b.peak:
		b.peak, b.hold = in, peakHold
	case b.hold > 0:
		b.hold -= dt
	default:
		b.peak = math.Max(b.level, b.peak-peakFall*sec)
	}
	if clipped || in >= 0 {
		b.clip = true
	}
}

// resting reports whether the channel shows nothing that still has to fall.
func (b ballistics) resting() bool { return b.level <= meterFloor && b.peak <= meterFloor }

// meterState is a stereo meter.
type meterState [2]ballistics

func newMeterState() meterState { return meterState{newBallistics(), newBallistics()} }

func (m *meterState) step(l liveaudio.Level, dt time.Duration) {
	for ch := range m {
		m[ch].step(ampToDB(l.Peak[ch]), l.Clipped && l.Peak[ch] >= 0.999, dt)
	}
	if l.Clipped && !m[0].clip && !m[1].clip {
		m[0].clip, m[1].clip = true, true
	}
}

func (m meterState) resting() bool    { return m[0].resting() && m[1].resting() }
func (m meterState) clipped() bool    { return m[0].clip || m[1].clip }
func (m meterState) maxPeak() float64 { return math.Max(m[0].peak, m[1].peak) }

func (m *meterState) resetClip() { m[0].clip, m[1].clip = false, false }

// levelMeter draws a stereo meter: clip LED on top, two segmented bars and dB ticks.
// Its bars span the same height as a fader next to it (pad at both ends).
type levelMeter struct {
	widget.BaseWidget
	state  meterState
	barW   float32
	labels bool
	pad    func() float32
}

const (
	ledH      = 4
	tickLabW  = 16
	meterGapX = 1
)

func newLevelMeter(barW float32, labels bool) *levelMeter {
	m := &levelMeter{state: newMeterState(), barW: barW, labels: labels, pad: faderEndPad}
	m.ExtendBaseWidget(m)
	return m
}

// Tapped resets the clip LED (and the peak lines).
func (m *levelMeter) Tapped(*fyne.PointEvent) {
	m.state.resetClip()
	for ch := range m.state {
		m.state[ch].peak, m.state[ch].hold = m.state[ch].level, 0
	}
	m.Refresh()
}

func (m *levelMeter) barsW() float32 { return 2*m.barW + meterGapX }

func (m *levelMeter) MinSize() fyne.Size {
	w := m.barsW()
	if m.labels {
		w += 3 + tickLabW
	}
	return fyne.NewSize(w, 120)
}

func (m *levelMeter) CreateRenderer() fyne.WidgetRenderer {
	r := &meterRenderer{m: m, led: canvas.NewRectangle(clipOff)}
	r.led.CornerRadius = 1
	r.bars = canvas.NewRaster(r.draw)
	r.bars.ScaleMode = canvas.ImageScalePixels
	if m.labels {
		for _, db := range meterTicks {
			l := kit.MonoText(strconv.Itoa(int(db)), studiotheme.Faint, 8)
			if db == 0 {
				l.Color = studiotheme.Muted
			}
			r.ticks = append(r.ticks, l)
		}
	}
	return r
}

type meterRenderer struct {
	m     *levelMeter
	led   *canvas.Rectangle
	bars  *canvas.Raster
	ticks []*canvas.Text
	size  fyne.Size
}

func (r *meterRenderer) Layout(s fyne.Size) {
	r.size = s
	pad := r.m.pad()
	bw := r.m.barsW()
	r.led.Move(fyne.NewPos(0, max(pad-ledH-3, 0)))
	r.led.Resize(fyne.NewSize(bw, ledH))
	r.bars.Move(fyne.NewPos(0, pad))
	r.bars.Resize(fyne.NewSize(bw, max(s.Height-2*pad, 1)))
	span := s.Height - 2*pad
	for i, t := range r.ticks {
		y := pad + float32(1-meterFrac(meterTicks[i]))*span
		t.Move(fyne.NewPos(bw+3, y-6))
		t.Resize(fyne.NewSize(tickLabW, 11))
	}
}

func (r *meterRenderer) MinSize() fyne.Size { return r.m.MinSize() }

func (r *meterRenderer) Refresh() {
	if r.m.state.clipped() {
		r.led.FillColor = meterRed
	} else {
		r.led.FillColor = clipOff
	}
	r.led.Refresh()
	r.bars.Refresh()
}

func (r *meterRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.led, r.bars}
	for _, t := range r.ticks {
		objs = append(objs, t)
	}
	return objs
}

func (r *meterRenderer) Destroy() {}

// draw renders both channels as LED segments: lit up to the level, dim above it, and a
// bright peak hold line.
func (r *meterRenderer) draw(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	if w <= 0 || h <= 0 {
		return img
	}
	st := r.m.state
	scale := float32(w) / r.m.barsW()
	bw := int(math.Round(float64(r.m.barW * scale)))
	gap := max(w-2*bw, 1)
	seg := max(int(math.Round(float64(3*scale))), 3) // 2 lit + 1 gap at scale 1
	// zone colour of every row, lit and dim
	lit := make([]color.NRGBA, h)
	dim := make([]color.NRGBA, h)
	dbOf := func(y int) float64 { return fracToDB(1 - (float64(y)+0.5)/float64(h)) }
	for y := range h {
		c := zoneColor(dbOf(y))
		lit[y] = c
		dim[y] = mix(meterBack, c, 0.16)
	}
	for ch := range 2 {
		x0 := ch * (bw + gap)
		x1 := min(x0+bw, w)
		top := int(math.Round((1 - meterFrac(st[ch].level)) * float64(h)))
		peakY := -1
		if st[ch].peak > meterFloor {
			peakY = min(int(math.Round((1-meterFrac(st[ch].peak))*float64(h))), h-1)
		}
		for y := range h {
			c := meterBack
			if (h-1-y)%seg != seg-1 {
				if y >= top && st[ch].level > meterFloor {
					c = lit[y]
				} else {
					c = dim[y]
				}
			}
			if peakY >= 0 && (y == peakY || y == peakY+1) {
				c = mix(lit[y], color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}, 0.25)
			}
			for x := x0; x < x1; x++ {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	return img
}

// fracToDB inverts meterFrac (0..1 to dBFS) for colouring rows.
func fracToDB(f float64) float64 {
	lo, hi := meterFloor, 0.0
	if f >= 1 {
		return 0.1
	}
	for range 30 {
		mid := (lo + hi) / 2
		if meterFrac(mid) < f {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}
