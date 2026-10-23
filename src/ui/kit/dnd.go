package kit

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

// In-app drag and drop of media items (media pool / library → timeline). Fyne has no
// internal DnD, so a drag source calls BeginMediaDrag/UpdateMediaDrag/EndMediaDrag from
// its Dragged/DragEnd handlers with absolute positions, and drop zones register here.
// UI thread only.

// DropZone accepts media dragged inside the window. Positions are relative to the zone.
type DropZone interface {
	fyne.CanvasObject
	MediaDragOver(ids []string, pos fyne.Position)
	MediaDragLeave()
	MediaDrop(ids []string, pos fyne.Position) bool
}

type mediaDrag struct {
	w     fyne.Window
	ids   []string
	ghost *widget.PopUp
	over  DropZone
}

var (
	dropZones = map[fyne.Window][]DropZone{}
	current   *mediaDrag
)

// RegisterDropZone makes z a drop target in window w until the returned func is called.
func RegisterDropZone(w fyne.Window, z DropZone) (unregister func()) {
	dropZones[w] = append(dropZones[w], z)
	return func() {
		zs := dropZones[w]
		for i, x := range zs {
			if x == z {
				dropZones[w] = append(zs[:i], zs[i+1:]...)
				break
			}
		}
	}
}

// BeginMediaDrag starts dragging ids with a small ghost label that follows the pointer.
func BeginMediaDrag(w fyne.Window, ids []string, label string) {
	CancelMediaDrag()
	if len(ids) == 0 {
		return
	}
	bg := canvas.NewRectangle(studiotheme.Raised)
	bg.CornerRadius = 4
	bg.StrokeColor = studiotheme.Steel
	bg.StrokeWidth = 1
	text := canvas.NewText(label, studiotheme.Text)
	text.TextSize = 12
	ghost := widget.NewPopUp(container.NewStack(bg, container.NewPadded(text)), w.Canvas())
	current = &mediaDrag{w: w, ids: append([]string(nil), ids...), ghost: ghost}
}

// MediaDragActive reports whether a media drag is in progress.
func MediaDragActive() bool { return current != nil }

// UpdateMediaDrag moves the ghost and notifies the zone under the absolute position.
func UpdateMediaDrag(abs fyne.Position) {
	d := current
	if d == nil {
		return
	}
	d.ghost.ShowAtPosition(abs.Add(fyne.NewPos(14, 10)))
	z, rel := zoneAt(d.w, abs)
	if d.over != nil && d.over != z {
		d.over.MediaDragLeave()
	}
	d.over = z
	if z != nil {
		z.MediaDragOver(d.ids, rel)
	}
}

// EndMediaDrag drops on the zone under abs, if any, and reports whether it was accepted.
func EndMediaDrag(abs fyne.Position) bool {
	d := current
	if d == nil {
		return false
	}
	CancelMediaDrag()
	z, rel := zoneAt(d.w, abs)
	if z == nil {
		return false
	}
	return z.MediaDrop(d.ids, rel)
}

// CancelMediaDrag aborts a drag without dropping.
func CancelMediaDrag() {
	d := current
	if d == nil {
		return
	}
	current = nil
	d.ghost.Hide()
	if d.over != nil {
		d.over.MediaDragLeave()
	}
}

func zoneAt(w fyne.Window, abs fyne.Position) (DropZone, fyne.Position) {
	drv := fyne.CurrentApp().Driver()
	zs := dropZones[w]
	for i := len(zs) - 1; i >= 0; i-- {
		z := zs[i]
		if !z.Visible() {
			continue
		}
		// Fyne walks only the visible tree and reports the origin for objects it cannot
		// find, so zones on hidden pages (never placed at the window origin) are skipped.
		pos := drv.AbsolutePositionForObject(z)
		if pos.IsZero() {
			continue
		}
		size := z.Size()
		rel := abs.Subtract(pos)
		if rel.X >= 0 && rel.Y >= 0 && rel.X < size.Width && rel.Y < size.Height {
			return z, rel
		}
	}
	return nil, fyne.Position{}
}
