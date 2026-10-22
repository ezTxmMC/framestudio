package edit

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/timeline"
	"framestudio-proto/src/ui/viewer/tip"
)

const minZoom, maxZoom = 1, 60

// toolbar sits in the timeline panel header: tools, edit commands, tracks, history, zoom.
type toolbar struct {
	pg      *Page
	content fyne.CanvasObject
	tips    *tip.Layer

	sel, blade, snap     *tip.Button
	split, del, ripple   *tip.Button
	clearRange, delTrack *tip.Button
	undo, redo           *tip.Button
	zoom                 *widget.Slider
	zoomLabel            *canvas.Text
	syncing              bool
}

func newToolbar(pg *Page) *toolbar {
	b := &toolbar{pg: pg}
	tips := tip.NewLayer(true)
	b.tips = tips
	btn := func(icon fyne.Resource, label string, tap func()) *tip.Button {
		return tips.Button(themed(icon), label, tap)
	}
	s := pg.ctx.Session
	b.sel = btn(iconSelect, "Auswahl (A)", func() { pg.setTool(timeline.ToolSelect) })
	b.blade = btn(iconBlade, "Klinge (B)", func() { pg.setTool(timeline.ToolBlade) })
	b.split = btn(iconSplit, "Teilen am Playhead (S)", pg.split)
	b.del = btn(theme.DeleteIcon(), "Clip löschen (Entf)", func() { pg.deleteSelected(false) })
	b.ripple = btn(iconRipple, "Löschen und Lücke schließen", func() { pg.deleteSelected(true) })
	marker := btn(iconMarker, "Marker setzen (M)", pg.addMarker)
	in := btn(iconIn, "In-Punkt setzen (I)", pg.markIn)
	out := btn(iconOut, "Out-Punkt setzen (O)", pg.markOut)
	b.clearRange = btn(iconClearRange, "Bereich löschen: In und Out entfernen", s.ClearRange)
	addV := btn(iconAddVideo, "Videospur hinzufügen", func() { pg.addTrack(model.TrackVideo) })
	addA := btn(iconAddAudio, "Audiospur hinzufügen", func() { pg.addTrack(model.TrackAudio) })
	b.delTrack = btn(iconRemoveTrack, "Ausgewählte Spur löschen", pg.removeSelectedTrack)
	b.snap = btn(iconSnap, "Einrasten", pg.toggleSnap)
	b.undo = btn(theme.ContentUndoIcon(), "Rückgängig", s.Undo)
	b.redo = btn(theme.ContentRedoIcon(), "Wiederholen", s.Redo)
	zoomOut := btn(theme.ZoomOutIcon(), "Herauszoomen (−)", func() { pg.zoomBy(1 / 1.5) })
	zoomIn := btn(theme.ZoomInIcon(), "Hineinzoomen (+)", func() { pg.zoomBy(1.5) })
	b.zoom = widget.NewSlider(minZoom, maxZoom)
	b.zoom.Step = 0.5
	b.zoom.OnChanged = func(v float64) {
		if !b.syncing {
			pg.setZoom(float32(v))
		}
	}
	zoomBox := container.NewGridWrap(fyne.NewSize(110, 30), b.zoom)

	bar := container.NewHBox(
		b.sel, b.blade, divider(),
		b.split, b.del, b.ripple, divider(),
		marker, in, out, b.clearRange, divider(),
		addV, addA, b.delTrack, divider(),
		b.snap, divider(),
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

// update mirrors tool, snapping, selection and history state.
func (b *toolbar) update() {
	pg, s := b.pg, b.pg.ctx.Session
	setIcon(b.sel, iconSelect, pg.tool == timeline.ToolSelect)
	setIcon(b.blade, iconBlade, pg.tool == timeline.ToolBlade)
	setIcon(b.snap, iconSnap, pg.snapping)
	enable(b.del, s.SelectedClip != "")
	enable(b.ripple, s.SelectedClip != "")
	enable(b.clearRange, s.HasRange() || s.In > 0 || s.Out > 0)
	enable(b.delTrack, s.SelectedTrack != "")
	enable(b.split, s.Project.Duration() > 0)
	enable(b.undo, s.CanUndo())
	enable(b.redo, s.CanRedo())
	b.undo.SetTip(withLabel("Rückgängig", s.UndoLabel()))
	b.redo.SetTip(withLabel("Wiederholen", s.RedoLabel()))
	b.syncing = true
	b.zoom.SetValue(float64(pg.zoom))
	b.syncing = false
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
