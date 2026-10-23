package edit

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/timeline"
	"framestudio-proto/src/ui/viewer/tip"
)

const minZoom, maxZoom = 1, 60

// toolDefs are the timeline tools of the segmented group, in display order.
var toolDefs = []struct {
	tool   timeline.Tool
	id     string
	icon   fyne.Resource
	label  string
	detail string
}{
	{timeline.ToolSelect, "tool.select", iconSelect, "Auswahl", ""},
	{timeline.ToolBlade, "tool.blade", iconBlade, "Klinge", ""},
	{timeline.ToolRipple, "tool.ripple", iconRippleTrim, "Ripple-Trim", "Kante ziehen verschiebt die folgenden Clips mit"},
	{timeline.ToolRoll, "tool.roll", iconRoll, "Rollen", "Schnitt zwischen zwei Clips verschieben"},
}

// toolbar sits in the timeline panel header: tools, edit commands, tracks, history, zoom.
// Every button runs a command (Page.run), so menus, keys and buttons share one state.
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

	// tipped are the buttons whose tooltip names their command's current shortcut
	tipped []tipped
}

// tipped is a button with a command tooltip: "label (key): detail".
type tipped struct {
	b             *tip.Button
	id            string
	label, detail string
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
	cmd := func(icon fyne.Resource, id, label, detail string) *tip.Button {
		btn := tips.Button(themed(icon), label, func() { pg.run(id) })
		b.tipped = append(b.tipped, tipped{btn, id, label, detail})
		return btn
	}
	seg := func(icon fyne.Resource, id, label, detail string) *segment {
		s := newSegment(tips, icon, label, func() { pg.run(id) })
		b.tipped = append(b.tipped, tipped{s.Button, id, label, detail})
		return s
	}
	s := pg.ctx.Session
	for _, d := range toolDefs {
		b.tools = append(b.tools, seg(d.icon, d.id, d.label, d.detail))
	}
	b.bladeAll = seg(iconBladeAll, "timeline.bladeAll", "Alle Spuren", "Klinge schneidet alle entsperrten Spuren")
	b.split = cmd(iconSplit, "clip.split", "Teilen am Playhead", "")
	b.del = cmd(theme.DeleteIcon(), "edit.delete", "Auswahl löschen", "")
	b.ripple = cmd(iconRipple, "edit.rippleDelete", "Löschen und Lücke schließen", "")
	marker := cmd(iconMarker, "marker.add", "Marker setzen", "")
	in := cmd(iconIn, "timeline.setIn", "In-Punkt setzen", "")
	out := cmd(iconOut, "timeline.setOut", "Out-Punkt setzen", "")
	b.clearRange = cmd(iconClearRange, "timeline.clearRange", "Bereich löschen", "In und Out entfernen")
	addV := cmd(iconAddVideo, "timeline.addVideoTrack", "Videospur hinzufügen", "")
	addA := cmd(iconAddAudio, "timeline.addAudioTrack", "Audiospur hinzufügen", "")
	b.delTrack = cmd(iconRemoveTrack, "timeline.deleteTrack", "Ausgewählte Spur löschen", "")
	b.snap = seg(iconSnap, "timeline.snapping", "Einrasten", "")
	// undo and redo are the shell's commands; the page has no binding for them
	b.undo = tips.Button(themed(theme.ContentUndoIcon()), "Rückgängig", s.Undo)
	b.redo = tips.Button(themed(theme.ContentRedoIcon()), "Wiederholen", s.Redo)
	zoomOut := cmd(theme.ZoomOutIcon(), "timeline.zoomOut", "Herauszoomen", "")
	zoomIn := cmd(theme.ZoomInIcon(), "timeline.zoomIn", "Hineinzoomen", "")
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

// tipText formats a command tooltip with the command's current shortcut.
func (b *toolbar) tipText(t tipped) string {
	s := t.label
	if sc := command.Display(b.pg.reg.Shortcut(t.id)); sc != "" {
		s += " (" + sc + ")"
	}
	if t.detail != "" {
		s += ": " + t.detail
	}
	return s
}

// updateTips follows key map changes and the selection count in the delete tips.
func (b *toolbar) updateTips() {
	n := len(b.pg.selection())
	for _, t := range b.tipped {
		switch {
		case t.b == b.del && n > 1:
			t.label = plural(n, "Clip", "Clips") + " löschen"
		case t.b == b.ripple && n > 1:
			t.label = plural(n, "Clip", "Clips") + " löschen und Lücken schließen"
		}
		if s := b.tipText(t); t.b.Tip != s {
			t.b.SetTip(s)
		}
	}
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
	enable(b.clearRange, s.HasRange() || s.In > 0 || s.Out > 0)
	enable(b.delTrack, s.SelectedTrack != "")
	enable(b.split, s.Project.Duration() > 0)
	enable(b.undo, s.CanUndo())
	enable(b.redo, s.CanRedo())
	b.undo.SetTip(withLabel("Rückgängig", s.UndoLabel()))
	b.redo.SetTip(withLabel("Wiederholen", s.RedoLabel()))
	b.updateTips()
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
