package viewer

import (
	"framestudio-proto/src/workgate"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"framestudio-proto/src/preview"
	"framestudio-proto/src/ui/kit"
)

// Texts of the warning chips (German UI).
const (
	ChipOverloaded = "Wiedergabe überlastet"
	ChipKeyframes  = "Nur Keyframes"
)

// chipHold keeps the overload chip visible after the playback recovered.
const chipHold = 3 * time.Second

// statsOverlay is the small readout in the corner of the picture (three monospace lines,
// refreshed at 2 Hz and only while visible) and the warning chips.
type statsOverlay struct {
	obj    *fyne.Container
	panel  *fyne.Container
	lines  [3]*canvas.Text
	chip   *fyne.Container
	chipTx *canvas.Text
	kf     *fyne.Container
	shown  bool
}

func newStatsOverlay() *statsOverlay {
	o := &statsOverlay{}
	bg := canvas.NewRectangle(color.NRGBA{A: 0x99})
	bg.CornerRadius = 3
	var objs []fyne.CanvasObject
	for i := range o.lines {
		t := canvas.NewText(" ", color.NRGBA{0xE8, 0xE8, 0xE8, 0xFF})
		t.TextSize = 11
		t.TextStyle = fyne.TextStyle{Monospace: true}
		o.lines[i] = t
		objs = append(objs, t)
	}
	o.panel = container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(3, 3, 6, 6), container.NewVBox(objs...)))
	o.panel.Hide()

	chip := func(text string) (*fyne.Container, *canvas.Text) {
		r := canvas.NewRectangle(color.NRGBA{0xE0, 0x9A, 0x1A, 0xE6})
		r.CornerRadius = 3
		t := canvas.NewText(text, color.NRGBA{0x10, 0x10, 0x10, 0xFF})
		t.TextSize = 11
		t.TextStyle = fyne.TextStyle{Bold: true}
		c := container.NewStack(r, container.New(layout.NewCustomPaddedLayout(2, 2, 7, 7), t))
		c.Hide()
		return c, t
	}
	o.chip, o.chipTx = chip(ChipOverloaded)
	o.kf, _ = chip(ChipKeyframes)
	top := container.NewBorder(nil, nil, o.panel, container.NewHBox(o.kf, o.chip))
	o.obj = container.New(layout.NewCustomPaddedLayout(8, 0, 8, 8), container.NewBorder(top, nil, nil, nil))
	return o
}

// update shows a snapshot. UI thread.
func (o *statsOverlay) update(sn preview.StatsSnapshot) {
	if o.shown {
		for i, l := range sn.Lines() {
			if o.lines[i].Text != l {
				o.lines[i].Text = l
				o.lines[i].Refresh()
			}
		}
	}
}

func setVisible(c *fyne.Container, on bool) {
	if c.Visible() == on {
		return
	}
	if on {
		c.Show()
	} else {
		c.Hide()
	}
}

// SetStats shows or hides the statistics readout (command view.previewStats).
func (v *Viewer) SetStats(on bool) {
	if v.overlay.shown == on {
		return
	}
	v.overlay.shown = on
	v.statsOn.Store(on)
	setVisible(v.overlay.panel, on)
	if on {
		v.overlay.update(v.sched.Stats().Snapshot(time.Now()))
		if v.s.Playing {
			v.startMonitor()
		}
	}
}

// StatsShown reports whether the readout is visible.
func (v *Viewer) StatsShown() bool { return v.overlay.shown }

// ChipShown reports whether the "Wiedergabe überlastet" chip is visible.
func (v *Viewer) ChipShown() bool { return v.overlay.chip.Visible() }

// ShowSnapshot applies a statistics snapshot to the readout and the chips. UI thread.
// The monitor calls it twice a second; tests call it with a fake snapshot.
func (v *Viewer) ShowSnapshot(sn preview.StatsSnapshot, now time.Time) {
	v.overlay.update(sn)
	if sn.UnderRun {
		v.chipTill = now.Add(chipHold)
	}
	on := now.Before(v.chipTill)
	v.chipOn.Store(on)
	setVisible(v.overlay.chip, on)
	setVisible(v.overlay.kf, sn.KFOnly)
}

// clearWarnings hides the chips when playback stops and shows the final numbers. UI thread.
func (v *Viewer) clearWarnings(sn preview.StatsSnapshot) {
	if v.playingA.Load() {
		return // playback resumed meanwhile; the new monitor owns the chips
	}
	v.chipTill = time.Time{}
	setVisible(v.overlay.chip, false)
	setVisible(v.overlay.kf, false)
	v.overlay.update(sn)
	v.chipOn.Store(false)
}

// startMonitor runs while playing: it probes the UI thread's queue latency every
// 100 ms (feeding the LoadGovernor), and refreshes the readout and chips twice a second
// when the readout is on or the playback is struggling. It ends when playback stops.
func (v *Viewer) startMonitor() {
	if v.closed || !v.monitor.CompareAndSwap(false, true) {
		return
	}
	quit := v.sched.Done()
	go func() {
		defer v.monitor.Store(false)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		v.sched.Stats().ResetRun(time.Now())
		n := 0
		for {
			select {
			case <-quit:
				return
			case <-tick.C:
			}
			n++
			if !v.playingA.Load() {
				v.gov.Reset()
				workgate.SetUIBusy(false)
				workgate.AllowVisibleWhilePlaying(false)
				// the paused picture must not keep a stale warning or stale numbers
				sn := v.sched.Stats().Snapshot(time.Now())
				kit.Do(func() { v.clearWarnings(sn) })
				return
			}
			if v.govern {
				v.gov.Probe(kit.Do)
				v.setBusy()
			}
			if n%5 == 0 {
				st := v.sched.Stats()
				st.SetUILatency(v.gov.P95())
				sn := st.Snapshot(time.Now())
				if v.statsOn.Load() || sn.UnderRun || v.chipOn.Load() || sn.KFOnly {
					kit.Do(func() { v.ShowSnapshot(sn, time.Now()) })
				}
			}
		}
	}()
}
