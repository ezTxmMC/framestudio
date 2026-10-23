package edit

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/timeline"
	"framestudio-proto/src/ui/viewer/tip"
)

const minZoom, maxZoom = 1, 60

// toolDefs are the timeline tools of the segmented group, in display order.
var toolDefs = []struct {
	tool timeline.Tool
	icon fyne.Resource
	tip  string
}{
	{timeline.ToolSelect, iconSelect, "Auswahl (A)"},
	{timeline.ToolBlade, iconBlade, "Klinge (B)"},
	{timeline.ToolRipple, iconRippleTrim, "Ripple-Trim (R): Kante ziehen verschiebt die folgenden Clips mit"},
	{timeline.ToolRoll, iconRoll, "Rollen (N): Schnitt zwischen zwei Clips verschieben"},
}

// toolbar sits in the timeline panel header: tools, edit commands, tracks, history, zoom.
type toolbar struct {
	pg      *Page
	content fyne.CanvasObject
	tips    *tip.Layer

	tools                []*segment // one per toolDefs entry
	bladeAll, snap       *segment
	split, del, ripple   *tip.Button
	clearRange, delTrack *tip.Button
	undo, redo           *tip.Button
	zoom                 *kit.Slider
	syncing              bool
}

// segment is a toggle button in a segmented group; the active one is backed in Steel.
type segment struct {
	*tip.Button
	bg   *canvas.Rectangle
	icon fyne.Resource
	obj  fyne.CanvasObject
	on   bool
}

func newSegment(tips *tip.Layer, icon fyne.Resource, label string, tap func()) *segment {
	s := &segment{Button: tips.Button(themed(icon), label, tap), icon: icon}
	s.bg = canvas.NewRectangle(color.Transparent)
	s.bg.CornerRadius = 3
	s.obj = container.NewStack(container.New(layout.NewCustomPaddedLayout(3, 3, 1, 1), s.bg), s.Button)
	return s
}

func (s *segment) set(on bool) {
	if s.on == on && s.Icon != nil {
		return
	}
	s.on = on
	s.bg.FillColor = color.Transparent
	if on {
		s.bg.FillColor = alpha(studiotheme.Steel, 0x48)
	}
	s.bg.Refresh()
	setIcon(s.Button, s.icon, on)
}

// group frames segments as one control.
func group(segs ...*segment) fyne.CanvasObject {
	frame := canvas.NewRectangle(studiotheme.Lane)
	frame.StrokeColor = studiotheme.Border
	frame.StrokeWidth = 1
	frame.CornerRadius = 4
	row := container.New(layout.NewCustomPaddedHBoxLayout(0))
	for _, s := range segs {
		row.Add(s.obj)
	}
	return container.NewCenter(container.NewStack(
		container.New(layout.NewCustomPaddedLayout(2, 2, 0, 0), frame),
		container.New(layout.NewCustomPaddedLayout(0, 0, 1, 1), row)))
}

func newToolbar(pg *Page) *toolbar {
	b := &toolbar{pg: pg}
	tips := tip.NewLayer(true)
	b.tips = tips
	btn := func(icon fyne.Resource, label string, tap func()) *tip.Button {
		return tips.Button(themed(icon), label, tap)
	}
	s := pg.ctx.Session
	for _, d := range toolDefs {
		t := d.tool
		b.tools = append(b.tools, newSegment(tips, d.icon, d.tip, func() { pg.setTool(t) }))
	}
	b.bladeAll = newSegment(tips, iconBladeAll, "Alle Spuren: Klinge schneidet alle entsperrten Spuren", func() {
		pg.setBladeAll(!pg.bladeAll)
	})
	b.split = btn(iconSplit, "Teilen am Playhead (S)", pg.split)
	b.del = btn(theme.DeleteIcon(), "Auswahl löschen (Entf)", func() { pg.deleteSelected(false) })
	b.ripple = btn(iconRipple, "Löschen und Lücke schließen", func() { pg.deleteSelected(true) })
	marker := btn(iconMarker, "Marker setzen (M)", pg.addMarker)
	in := btn(iconIn, "In-Punkt setzen (I)", pg.markIn)
	out := btn(iconOut, "Out-Punkt setzen (O)", pg.markOut)
	b.clearRange = btn(iconClearRange, "Bereich löschen: In und Out entfernen", s.ClearRange)
	addV := btn(iconAddVideo, "Videospur hinzufügen", func() { pg.addTrack(model.TrackVideo) })
	addA := btn(iconAddAudio, "Audiospur hinzufügen", func() { pg.addTrack(model.TrackAudio) })
	b.delTrack = btn(iconRemoveTrack, "Ausgewählte Spur löschen", pg.removeSelectedTrack)
	b.snap = newSegment(tips, iconSnap, "Einrasten", pg.toggleSnap)
	b.undo = btn(theme.ContentUndoIcon(), "Rückgängig", s.Undo)
	b.redo = btn(theme.ContentRedoIcon(), "Wiederholen", s.Redo)
	zoomOut := btn(theme.ZoomOutIcon(), "Herauszoomen (−)", func() { pg.zoomBy(1 / 1.5) })
	zoomIn := btn(theme.ZoomInIcon(), "Hineinzoomen (+)", func() { pg.zoomBy(1.5) })
	b.zoom = kit.NewSlider(minZoom, maxZoom)
	b.zoom.Step = 0.5
	b.zoom.OnChanged = func(v float64) {
		if !b.syncing {
			pg.setZoom(float32(v))
		}
	}
	zoomBox := container.NewGridWrap(fyne.NewSize(110, 30), b.zoom)

	bar := container.NewHBox(
		group(b.tools...), group(b.bladeAll), divider(),
		b.split, b.del, b.ripple, divider(),
		marker, in, out, b.clearRange, divider(),
		addV, addA, b.delTrack, divider(),
		group(b.snap), divider(),
		b.undo, b.redo, divider(),
		zoomOut, container.NewCenter(zoomBox), zoomIn,
	)
	b.content = container.NewStack(bar, tips)
	b.update()
	return b
}

func divider() fyne.CanvasObject {
	line := canvas.NewRectangle(studiotheme.Border)
	line.SetMinSize(fyne.NewSize(1, 18))
	return container.NewCenter(line)
}

// update mirrors tool, snapping, selection, history and zoom state.
func (b *toolbar) update() {
	pg, s := b.pg, b.pg.ctx.Session
	for i, d := range toolDefs {
		b.tools[i].set(pg.tool == d.tool)
	}
	b.bladeAll.set(pg.bladeAll)
	b.snap.set(pg.snapping)
	n := len(pg.selection())
	enable(b.del, n > 0)
	enable(b.ripple, n > 0)
	b.del.SetTip("Auswahl löschen (Entf)")
	b.ripple.SetTip("Löschen und Lücke schließen")
	if n > 1 {
		b.del.SetTip(plural(n, "Clip", "Clips") + " löschen (Entf)")
		b.ripple.SetTip(plural(n, "Clip", "Clips") + " löschen und Lücken schließen")
	}
	enable(b.clearRange, s.HasRange() || s.In > 0 || s.Out > 0)
	enable(b.delTrack, s.SelectedTrack != "")
	enable(b.split, s.Project.Duration() > 0)
	enable(b.undo, s.CanUndo())
	enable(b.redo, s.CanRedo())
	b.undo.SetTip(withLabel("Rückgängig", s.UndoLabel()))
	b.redo.SetTip(withLabel("Wiederholen", s.RedoLabel()))
	if b.zoom.Value != float64(pg.zoom) {
		b.syncing = true
		b.zoom.SetValue(float64(pg.zoom))
		b.syncing = false
	}
}

func withLabel(action, label string) string {
	if label == "" {
		return action
	}
	return action + ": " + label
}

func setIcon(b *tip.Button, icon fyne.Resource, active bool) {
	res := themed(icon)
	if active {
		res = accent(icon)
	}
	if b.Icon == nil || b.Icon.Name() != res.Name() {
		b.Icon = res
		b.Refresh()
	}
}

func enable(b *tip.Button, on bool) {
	if on == b.Disabled() {
		if on {
			b.Enable()
		} else {
			b.Disable()
		}
	}
}
