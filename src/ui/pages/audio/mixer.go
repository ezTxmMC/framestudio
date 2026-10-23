package audio

import (
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/liveaudio"
	studiotheme "framestudio-proto/src/ui/theme"
)

const (
	stripW  = 116
	masterW = 104
)

// mixer shows one channel strip per audio track, scrolling horizontally, and the master
// strip pinned to the right.
type mixer struct {
	pg     *Page
	strips map[string]*strip
	order  []string
	box    *fyne.Container
	empty  *fyne.Container
	master *masterStrip
	obj    fyne.CanvasObject
}

func newMixer(pg *Page) *mixer {
	m := &mixer{pg: pg, strips: map[string]*strip{}, box: container.NewHBox()}
	hint := canvas.NewText("Keine Audiospuren", studiotheme.Muted)
	hint.TextSize = 12
	m.empty = container.NewCenter(hint)
	m.master = newMasterStrip()
	m.obj = container.NewBorder(nil, nil, nil, m.master.obj,
		container.NewStack(container.NewHScroll(m.box), m.empty))
	return m
}

func (m *mixer) update() {
	p := m.pg.s.Project
	var ids []string
	for _, ti := range p.TrackIndices(model.TrackAudio) {
		ids = append(ids, p.Tracks[ti].ID)
	}
	if !slices.Equal(ids, m.order) {
		objs := make([]fyne.CanvasObject, 0, len(ids))
		for _, id := range ids {
			st := m.strips[id]
			if st == nil {
				st = newStrip(m.pg, id)
				m.strips[id] = st
			}
			objs = append(objs, st.obj)
		}
		for id := range m.strips {
			if !slices.Contains(ids, id) {
				delete(m.strips, id)
			}
		}
		m.order = ids
		m.box.Objects = objs
		m.box.Refresh()
	}
	for _, st := range m.strips {
		st.update()
	}
	if len(ids) == 0 {
		m.empty.Show()
	} else {
		m.empty.Hide()
	}
}

// stepMeters feeds new levels into every meter and redraws them.
func (m *mixer) stepMeters(master liveaudio.Level, tracks map[string]liveaudio.Level, dt time.Duration) {
	for id, st := range m.strips {
		st.meter.state.step(tracks[id], dt)
		st.meter.Refresh()
	}
	m.master.meter.state.step(master, dt)
	m.master.meter.Refresh()
	m.master.updateReadout()
}

// resting reports whether all meters are down.
func (m *mixer) resting() bool {
	for _, st := range m.strips {
		if !st.meter.state.resting() {
			return false
		}
	}
	return m.master.meter.state.resting()
}

// resetMeters drops all meters to rest at once (page hidden).
func (m *mixer) resetMeters() {
	for _, st := range m.strips {
		st.meter.state = newMeterState()
		st.meter.Refresh()
	}
	m.master.meter.state = newMeterState()
	m.master.meter.Refresh()
	m.master.updateReadout()
}

// strip is a channel strip: name, mute/solo, fader with dB scale, meter and readout.
type strip struct {
	pg         *Page
	id         string
	name       *nameLabel
	bg         *canvas.Rectangle
	mute, solo *toggle
	fader      *kit.Slider
	meter      *levelMeter
	readout    *canvas.Text
	obj        fyne.CanvasObject
}

func newStrip(pg *Page, id string) *strip {
	st := &strip{pg: pg, id: id}
	st.bg = canvas.NewRectangle(studiotheme.Raised)
	st.bg.CornerRadius = 4
	st.bg.StrokeWidth = 1
	st.name = newNameLabel(13, fyne.TextAlignCenter, func() { pg.s.SelectTrack(id) },
		func(name string) { pg.renameTrack(id, name) })
	st.name.fullName = func() string { return pg.track(id).Name }
	st.mute = newToggle("M", accent, func() { pg.toggleMute(id) })
	st.solo = newToggle("S", amber, func() { pg.toggleSolo(id) })

	st.fader = kit.NewSlider(MinDB, MaxDB)
	st.fader.Orientation = widget.Vertical
	st.fader.Step = 0.5
	st.fader.Value = 0
	st.fader.OnChanged = func(db float64) {
		st.readout.Text = FormatDB(db)
		st.readout.Refresh()
		pg.setTrackVolume(id, db)
	}
	st.meter = newLevelMeter(5, true)

	st.readout = kit.MonoText("0,0 dB", studiotheme.Text, 11)
	st.readout.Alignment = fyne.TextAlignCenter
	readoutBg := canvas.NewRectangle(studiotheme.Lane)
	readoutBg.CornerRadius = 3
	readout := container.NewStack(readoutBg, fixedSize(0, 22), container.NewCenter(st.readout))

	head := container.NewVBox(
		container.NewStack(fixedSize(0, 20), st.name),
		container.NewCenter(container.NewHBox(st.mute, st.solo)))
	fader := container.New(faderLayout{}, newDBScale(),
		container.NewThemeOverride(st.fader, faderTheme{studiotheme.New()}), st.meter)
	body := container.NewBorder(head, readout, nil, nil, fader)
	st.obj = container.NewStack(fixedSize(stripW, 0), st.bg,
		container.New(layout.NewCustomPaddedLayout(8, 8, 6, 6), body))
	return st
}

func (st *strip) update() {
	t, _ := st.pg.s.Project.Track(st.id)
	if t == nil {
		return
	}
	st.name.SetText(fitText(t.Name, 13, fyne.TextStyle{Bold: true}, stripW-16))
	st.mute.SetOn(t.Muted)
	st.solo.SetOn(t.Solo)
	db := LinearToDB(t.Volume)
	if math.Abs(st.fader.Value-db) > 0.01 {
		st.fader.Value = db // set directly: SetValue would fire OnChanged and record an edit
		st.fader.Refresh()
	}
	st.readout.Text = FormatDB(db)
	st.readout.Refresh()
	if st.pg.s.SelectedTrack == st.id {
		st.bg.StrokeColor = studiotheme.Steel
	} else {
		st.bg.StrokeColor = studiotheme.Border
	}
	st.bg.Refresh()
}

// masterStrip shows the master bus meter with its peak readout. The project has no master
// gain, so there is no fader.
type masterStrip struct {
	meter   *levelMeter
	readout *canvas.Text
	obj     fyne.CanvasObject
}

func newMasterStrip() *masterStrip {
	ms := &masterStrip{meter: newLevelMeter(10, true)}
	bg := canvas.NewRectangle(studiotheme.Raised)
	bg.CornerRadius = 4
	bg.StrokeWidth = 1
	bg.StrokeColor = studiotheme.Border
	title := canvas.NewText("Master", studiotheme.Text)
	title.TextSize = 13
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.Alignment = fyne.TextAlignCenter
	sub := canvas.NewText("Stereo", studiotheme.Muted)
	sub.TextSize = 11
	sub.Alignment = fyne.TextAlignCenter

	ms.readout = kit.MonoText("-∞ dB", studiotheme.Text, 11)
	ms.readout.Alignment = fyne.TextAlignCenter
	readoutBg := canvas.NewRectangle(studiotheme.Lane)
	readoutBg.CornerRadius = 3
	readout := container.NewStack(readoutBg, fixedSize(0, 22), container.NewCenter(ms.readout))

	head := container.NewVBox(container.NewStack(fixedSize(0, 20), title),
		container.NewStack(fixedSize(0, 19), sub))
	body := container.NewBorder(head, readout, nil, nil, container.New(hCenter{}, ms.meter))
	vline := canvas.NewRectangle(studiotheme.Border)
	vline.SetMinSize(fyne.NewSize(1, 0))
	ms.obj = container.NewBorder(nil, nil, container.NewHBox(fixedSize(4, 0), vline, fixedSize(6, 0)), nil,
		container.NewStack(fixedSize(masterW, 0), bg, container.New(layout.NewCustomPaddedLayout(8, 8, 6, 6), body)))
	return ms
}

// updateReadout shows the held master peak, e.g. "-3,2 dBFS".
func (ms *masterStrip) updateReadout() {
	pk := ms.meter.state.maxPeak()
	text := "-∞ dB"
	if pk > meterFloor {
		text = strings.Replace(fmt.Sprintf("%.1f dB", math.Min(pk, 0)), ".", ",", 1)
	}
	if ms.meter.state.clipped() {
		ms.readout.Color = meterRed
	} else {
		ms.readout.Color = studiotheme.Text
	}
	setText(ms.readout, text)
	ms.readout.Refresh()
}

// faderEndPad is the distance from the slider ends to its track ends, mirroring widget.Slider.
func faderEndPad() float32 {
	th := fyne.CurrentApp().Settings().Theme()
	return (th.Size(theme.SizeNameInlineIcon)-4)/2 + th.Size(theme.SizeNameInnerPadding) - 1.5
}

// faderLayout places the dB scale, the vertical slider and the meter side by side.
type faderLayout struct{}

const (
	scaleW   = 26
	meterGap = 3
)

func (faderLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	sw := objs[1].MinSize().Width
	mw := objs[2].MinSize().Width
	total := scaleW + sw + meterGap + mw
	x := (size.Width - total) / 2
	objs[0].Move(fyne.NewPos(x, 0))
	objs[0].Resize(fyne.NewSize(scaleW, size.Height))
	objs[1].Move(fyne.NewPos(x+scaleW-2, 0))
	objs[1].Resize(fyne.NewSize(sw, size.Height))
	objs[2].Move(fyne.NewPos(x+scaleW+sw+meterGap, 0))
	objs[2].Resize(fyne.NewSize(mw, size.Height))
}

func (faderLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(scaleW+objs[1].MinSize().Width+meterGap+objs[2].MinSize().Width, 120)
}

// dBScale draws fader marks aligned with the slider track.
type dBScale struct{ widget.BaseWidget }

func newDBScale() *dBScale {
	d := &dBScale{}
	d.ExtendBaseWidget(d)
	return d
}

var scaleMarks = []float64{12, 6, 0, -6, -12, -18, -24, -36, -48, -60}

func (d *dBScale) CreateRenderer() fyne.WidgetRenderer {
	return newDrawnRenderer(d, fyne.NewSize(scaleW, 120), func(size fyne.Size) []fyne.CanvasObject {
		pad := faderEndPad()
		span := size.Height - 2*pad
		var objs []fyne.CanvasObject
		for _, db := range scaleMarks {
			y := pad + float32((MaxDB-db)/(MaxDB-MinDB))*span
			label := fmt.Sprintf("%+.0f", db)
			if db == 0 {
				label = "0"
			} else if db <= MinDB {
				label = "-∞"
			}
			c := color.Color(studiotheme.Faint)
			if db == 0 {
				c = studiotheme.Muted
			}
			t := kit.MonoText(label, c, 9)
			t.Alignment = fyne.TextAlignTrailing
			objs = append(objs, place(t, 0, y-7, size.Width-7, 13),
				place(canvas.NewRectangle(c), size.Width-5, y, 4, 1))
		}
		return objs
	})
}

// faderTheme draws fader caps and fills in a quieter grey than regular sliders.
type faderTheme struct{ fyne.Theme }

func (t faderTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if n == theme.ColorNameForeground {
		return color.NRGBA{0xA4, 0xA7, 0xAE, 0xFF}
	}
	return t.Theme.Color(n, v)
}
