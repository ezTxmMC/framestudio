package audio

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// drawnRenderer rebuilds its canvas objects from a build func on every layout or refresh.
// Used for the custom-drawn parts of the page (ruler, lanes, playhead), which are cheap to redo.
type drawnRenderer struct {
	w     fyne.Widget
	build func(size fyne.Size) []fyne.CanvasObject
	min   fyne.Size
	objs  []fyne.CanvasObject
}

func newDrawnRenderer(w fyne.Widget, min fyne.Size, build func(fyne.Size) []fyne.CanvasObject) *drawnRenderer {
	return &drawnRenderer{w: w, build: build, min: min}
}

func (r *drawnRenderer) Layout(s fyne.Size)           { r.objs = r.build(s) }
func (r *drawnRenderer) MinSize() fyne.Size           { return r.min }
func (r *drawnRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *drawnRenderer) Destroy()                     {}

func (r *drawnRenderer) Refresh() {
	r.Layout(r.w.Size())
	canvas.Refresh(r.w)
}

func place(o fyne.CanvasObject, x, y, w, h float32) fyne.CanvasObject {
	o.Move(fyne.NewPos(x, y))
	o.Resize(fyne.NewSize(w, h))
	return o
}
