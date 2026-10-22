package audio

import (
	"fmt"
	"image/color"
	"math"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

const stripW = 96

// mixer shows one channel strip per audio track, scrolling horizontally.
type mixer struct {
	pg     *Page
	strips map[string]*strip
	order  []string
	box    *fyne.Container
	empty  *fyne.Container
	obj    fyne.CanvasObject
}

func newMixer(pg *Page) *mixer {
	m := &mixer{pg: pg, strips: map[string]*strip{}, box: container.NewHBox()}
	hint := canvas.NewText("Keine Audiospuren", studiotheme.Muted)
	hint.TextSize = 12
	m.empty = container.NewCenter(hint)
	m.obj = container.NewStack(container.NewHScroll(m.box), m.empty)
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

// strip is a channel strip: name, mute/solo, fader with dB scale and readout.
type strip struct {
	pg         *Page
	id         string
	name       *canvas.Text
	bg         *canvas.Rectangle
	mute, solo *toggle
	fader      *widget.Slider
	readout    *canvas.Text
	obj        fyne.CanvasObject
}

func newStrip(pg *Page, id string) *strip {
	st := &strip{pg: pg, id: id}
	st.bg = canvas.NewRectangle(studiotheme.Raised)
	st.bg.CornerRadius = 4
	st.bg.StrokeWidth = 1
	st.name = canvas.NewText("", studiotheme.Text)
	st.name.TextSize = 13
	st.name.TextStyle = fyne.TextStyle{Bold: true}
	st.name.Alignment = fyne.TextAlignCenter
	st.mute = newToggle("M", studiotheme.Steel, func() { pg.toggleMute(id) })
	st.solo = newToggle("S", amber, func() { pg.toggleSolo(id) })

	st.fader = widget.NewSlider(MinDB, MaxDB)
	st.fader.Orientation = widget.Vertical
	st.fader.Step = 0.5
	st.fader.Value = 0
	st.fader.OnChanged = func(db float64) {
		st.readout.Text = FormatDB(db)
		st.readout.Refresh()
		pg.setTrackVolume(id, db)
	}

	st.readout = kit.MonoText("0,0 dB", studiotheme.Text, 11)
	st.readout.Alignment = fyne.TextAlignCenter
	readoutBg := canvas.NewRectangle(studiotheme.Lane)
	readoutBg.CornerRadius = 3
	readout := container.NewStack(readoutBg, fixedSize(0, 22), container.NewCenter(st.readout))

	head := container.NewVBox(
		newTapArea(container.NewStack(fixedSize(0, 20), st.name), func() { pg.s.SelectTrack(id) }),
		container.NewCenter(container.NewHBox(st.mute, st.solo)))
	fader := container.New(faderLayout{}, newDBScale(), container.NewThemeOverride(st.fader, faderTheme{studiotheme.New()}))
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
	st.name.Text = fitText(t.Name, 13, st.name.TextStyle, stripW-16)
	st.name.Refresh()
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

// faderEndPad is the distance from the slider ends to its track ends, mirroring widget.Slider.
func faderEndPad() float32 {
	th := fyne.CurrentApp().Settings().Theme()
	return (th.Size(theme.SizeNameInlineIcon)-4)/2 + th.Size(theme.SizeNameInnerPadding) - 1.5
}

// faderLayout centres the dB scale and the vertical slider side by side.
type faderLayout struct{}

const scaleW = 30

func (faderLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	sw := objs[1].MinSize().Width
	x := (size.Width-scaleW-sw)/2 + 4
	objs[0].Move(fyne.NewPos(x-6, 0))
	objs[0].Resize(fyne.NewSize(scaleW, size.Height))
	objs[1].Move(fyne.NewPos(x+scaleW-6, 0))
	objs[1].Resize(fyne.NewSize(sw, size.Height))
}

func (faderLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(scaleW+objs[1].MinSize().Width, 120)
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
