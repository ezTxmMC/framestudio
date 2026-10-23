package color

import (
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// scopeIDs are the names of the scope kinds in layouts and command IDs.
var scopeIDs = []string{"histogram", "waveform", "vectorscope"}

func (k scopeKind) id() string { return scopeIDs[k] }

// parseScope returns the scope kind with the given ID.
func parseScope(id string) (scopeKind, bool) {
	for i, s := range scopeIDs {
		if s == id {
			return scopeKind(i), true
		}
	}
	return 0, false
}

// scopeView is the "Scopes" panel: a segmented scope choice and the scope image. The
// colour page and the detachable scopes panel each own one; frames come from outside
// (the page viewer or the panel's own frameSource) through submit.
type scopeView struct {
	kind    scopeKind
	btns    []*widget.Button
	img     *canvas.Image
	waiting fyne.CanvasObject
	scale   *fyne.Container
	runner  *scopeRunner
	// onKind is called after the user picked another scope.
	onKind func(k scopeKind)
	object fyne.CanvasObject
}

// newScopeView builds the panel; tools (may be nil) sits in the header left of the
// scope buttons.
func newScopeView(async func(func()), tools fyne.CanvasObject) *scopeView {
	v := &scopeView{}
	v.runner = &scopeRunner{async: async, show: v.show}
	seg := container.NewHBox()
	if tools != nil {
		seg.Add(tools)
	}
	for i, name := range scopeNames {
		k := scopeKind(i)
		b := widget.NewButton(name, func() { v.setKind(k) })
		v.btns = append(v.btns, b)
		seg.Add(b)
	}
	v.updateButtons()

	v.img = canvas.NewImageFromImage(nil)
	v.img.FillMode = canvas.ImageFillContain
	v.img.ScaleMode = canvas.ImageScaleSmooth
	v.img.Hide()
	bg := canvas.NewRectangle(scopeBG)
	bg.CornerRadius = 4
	waiting := canvas.NewText("Kein Bild – Playhead auf einen Clip setzen", studiotheme.Faint)
	waiting.TextSize = 12
	v.waiting = container.NewCenter(waiting)
	v.scale = container.New(waveScaleLayout{})
	for _, l := range []string{"100", "75", "50", "25", "0"} {
		v.scale.Add(kit.MonoText(l, studiotheme.Faint, 9))
	}
	v.scale.Hide()
	v.object = kit.Panel("Scopes", seg, container.NewStack(bg,
		container.New(layout.NewCustomPaddedLayout(6, 6, 6, 6), v.img, v.scale), v.waiting))
	return v
}

// setKind switches the scope and recomputes it from the latest frame.
func (v *scopeView) setKind(k scopeKind) {
	if v.kind == k {
		return
	}
	v.kind = k
	v.updateButtons()
	v.img.Image = nil
	v.img.Hide()
	v.scale.Hide()
	if f := v.runner.latest(); f != nil {
		v.runner.submit(f, k)
	}
	if v.onKind != nil {
		v.onKind(k)
	}
}

func (v *scopeView) updateButtons() {
	for i, b := range v.btns {
		imp := widget.LowImportance
		if scopeKind(i) == v.kind {
			imp = widget.HighImportance
		}
		if b.Importance != imp {
			b.Importance = imp
			b.Refresh()
		}
	}
}

// submit computes the current scope of frame f in the background.
func (v *scopeView) submit(f *image.NRGBA) { v.runner.submit(f, v.kind) }

// resubmit recomputes the scope of the latest frame (after the view was hidden).
func (v *scopeView) resubmit() {
	if f := v.runner.latest(); f != nil {
		v.runner.submit(f, v.kind)
	}
}

func (v *scopeView) show(k scopeKind, img *image.NRGBA) {
	if k != v.kind {
		return
	}
	v.img.Image = img
	v.img.Show()
	if k == scopeWaveform {
		v.scale.Show()
	} else {
		v.scale.Hide()
	}
	v.img.Refresh()
	v.waiting.Hide()
}

func (v *scopeView) close() { v.runner.close() }
