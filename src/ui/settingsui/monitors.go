package settingsui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/display"
	studiotheme "framestudio-proto/src/ui/theme"
)

// monitorsFunc and friends read the display state; tests replace them.
var (
	monitorsFunc    = display.Monitors
	platformFunc    = display.Platform
	canPositionFunc = display.CanPosition
)

// monitorOverview shows the window system, whether windows can be placed freely, a map
// of the connected monitors and the way to the monitor dialog.
type monitorOverview struct {
	w        *window
	box      *fyne.Container
	platform *canvas.Text
	position *canvas.Text
	posHelp  *canvas.Text
	desk     *fyne.Container // the monitor map
	list     *fyne.Container
	open     *widget.Button
}

func newMonitorOverview(w *window) *monitorOverview {
	o := &monitorOverview{w: w}
	o.platform = canvas.NewText("", studiotheme.Text)
	o.platform.TextSize = 13
	o.position = canvas.NewText("", studiotheme.Text)
	o.position.TextSize = 13
	o.posHelp = canvas.NewText("", studiotheme.Muted)
	o.posHelp.TextSize = 11
	o.desk = container.NewWithoutLayout()
	o.list = container.NewVBox()
	o.open = widget.NewButtonWithIcon("Monitore und Fenster einrichten…", iconMonitors, func() {
		if w.reg != nil {
			w.reg.Run("workspace.monitors")
		}
	})
	o.open.Importance = widget.HighImportance
	rescan := widget.NewButtonWithIcon("Neu einlesen", theme.ViewRefreshIcon(), o.refresh)
	rescan.Importance = widget.LowImportance

	label := func(s string) fyne.CanvasObject {
		t := canvas.NewText(s, studiotheme.Muted)
		t.TextSize = 12
		return container.New(&fixedWidth{w: 190}, t)
	}
	facts := container.New(&vstack{gap: 6},
		container.NewBorder(nil, nil, label("Fenstersystem"), nil, o.platform),
		container.NewBorder(nil, nil, label("Fenster frei platzierbar"), nil, container.New(&vstack{gap: 2}, o.position, o.posHelp)))

	deskBG := canvas.NewRectangle(studiotheme.Lane)
	deskBG.CornerRadius = 4
	deskBG.StrokeColor = studiotheme.Border
	deskBG.StrokeWidth = 1
	deskArea := container.New(&deskLayout{o: o}, deskBG, o.desk)

	title := canvas.NewText("BILDSCHIRME", studiotheme.Muted)
	title.TextSize = 11
	title.TextStyle = fyne.TextStyle{Bold: true}
	o.box = container.New(layout.NewCustomPaddedLayout(14, 4, rowPadX, rowPadX), container.New(&vstack{gap: 10},
		container.NewBorder(nil, nil, nil, rescan, container.New(vcenter{}, title)),
		facts,
		deskArea,
		o.list,
		container.NewHBox(o.open),
	))
	o.refresh()
	return o
}

func (o *monitorOverview) view() fyne.CanvasObject { return o.box }

func (o *monitorOverview) refresh() {
	switch p := platformFunc(); p {
	case "wayland":
		o.platform.Text = "Wayland"
	case "x11":
		o.platform.Text = "X11"
	case "":
		o.platform.Text = "Unbekannt"
	default:
		o.platform.Text = p
	}
	if canPositionFunc() {
		o.position.Text = "Ja"
		o.posHelp.Text = "Panels lassen sich als Fenster oder im Vollbild auf jeden Monitor legen."
	} else {
		o.position.Text = "Nur im Vollbild"
		o.posHelp.Text = "Für frei platzierte Fenster „Fenstersystem“ auf X11 stellen."
	}
	o.platform.Refresh()
	o.position.Refresh()
	o.posHelp.Refresh()

	mons := monitorsFunc()
	o.list.Objects = nil
	if len(mons) == 0 {
		t := canvas.NewText("Keine Monitorinformationen verfügbar.", studiotheme.Muted)
		t.TextSize = 12
		o.list.Add(t)
	}
	for _, m := range mons {
		o.list.Add(monitorRow(m))
	}
	o.list.Refresh()
	o.desk.Objects = nil
	for _, m := range mons {
		o.desk.Add(newMonitorTile(m))
	}
	o.desk.Refresh()
	o.box.Refresh()
	if o.w.reg != nil {
		if c, ok := o.w.reg.Get("workspace.monitors"); ok && c.Available && c.Enabled {
			o.open.Enable()
			return
		}
	}
	o.open.Disable()
}

func monitorRow(m display.Monitor) fyne.CanvasObject {
	id := canvas.NewText(m.ID, studiotheme.Text)
	id.TextSize = 12
	id.TextStyle = fyne.TextStyle{Monospace: true}
	mode := fmt.Sprintf("%d × %d", m.W, m.H)
	if m.Refresh > 0 {
		mode += fmt.Sprintf(" · %d Hz", m.Refresh)
	}
	if m.Scale > 0 && m.Scale != 1 {
		mode += fmt.Sprintf(" · Skalierung %.0f %%", m.Scale*100)
	}
	info := canvas.NewText(mode, studiotheme.Text)
	info.TextSize = 12
	pos := canvas.NewText(fmt.Sprintf("Position %d, %d", m.X, m.Y), studiotheme.Muted)
	pos.TextSize = 12
	var right fyne.CanvasObject = layout.NewSpacer()
	if m.Primary {
		right = badge("Hauptmonitor", studiotheme.Steel)
	}
	line := canvas.NewRectangle(studiotheme.Border)
	line.SetMinSize(fyne.NewSize(1, 1))
	return container.NewVBox(container.New(layout.NewCustomPaddedLayout(3, 3, 0, 0),
		container.NewBorder(nil, nil, container.New(&fixedWidth{w: 110}, id), right,
			container.NewGridWithColumns(2, info, pos))), line)
}

// monitorTile draws one monitor in the desktop map.
type monitorTile struct {
	widget.BaseWidget
	m display.Monitor
}

func newMonitorTile(m display.Monitor) *monitorTile {
	t := &monitorTile{m: m}
	t.ExtendBaseWidget(t)
	return t
}

func (t *monitorTile) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(studiotheme.Raised)
	bg.CornerRadius = 3
	bg.StrokeWidth = 1.5
	bg.StrokeColor = studiotheme.Border
	if t.m.Primary {
		bg.StrokeColor = studiotheme.Steel
	}
	id := canvas.NewText(t.m.ID, studiotheme.Text)
	id.TextSize = 12
	id.TextStyle = fyne.TextStyle{Bold: true}
	id.Alignment = fyne.TextAlignCenter
	res := canvas.NewText(fmt.Sprintf("%d×%d", t.m.W, t.m.H), studiotheme.Muted)
	res.TextSize = 10.5
	res.Alignment = fyne.TextAlignCenter
	return widget.NewSimpleRenderer(container.NewStack(bg, container.NewCenter(container.New(&vstack{gap: 1}, id, res))))
}

// deskLayout scales the virtual desktop into the map area.
type deskLayout struct{ o *monitorOverview }

const deskH = 120

func (l *deskLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	objs[0].Resize(size)
	objs[0].Move(fyne.NewPos(0, 0))
	desk := objs[1].(*fyne.Container)
	desk.Resize(size)
	desk.Move(fyne.NewPos(0, 0))
	var minX, minY, maxX, maxY int
	for i, o := range desk.Objects {
		m := o.(*monitorTile).m
		if i == 0 || m.X < minX {
			minX = m.X
		}
		if i == 0 || m.Y < minY {
			minY = m.Y
		}
		if i == 0 || m.X+m.W > maxX {
			maxX = m.X + m.W
		}
		if i == 0 || m.Y+m.H > maxY {
			maxY = m.Y + m.H
		}
	}
	if maxX <= minX || maxY <= minY {
		return
	}
	const pad = 12
	avW, avH := size.Width-2*pad, size.Height-2*pad
	sc := min(avW/float32(maxX-minX), avH/float32(maxY-minY))
	offX := pad + (avW-sc*float32(maxX-minX))/2
	offY := pad + (avH-sc*float32(maxY-minY))/2
	const gap = 3
	for _, o := range desk.Objects {
		m := o.(*monitorTile).m
		o.Move(fyne.NewPos(offX+sc*float32(m.X-minX)+gap, offY+sc*float32(m.Y-minY)+gap))
		o.Resize(fyne.NewSize(sc*float32(m.W)-2*gap, sc*float32(m.H)-2*gap))
	}
}

func (l *deskLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(200, deskH) }
