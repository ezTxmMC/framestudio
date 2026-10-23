package viewer

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"framestudio-proto/src/settings"
)

// Preview qualities (settings Playback.PreviewQuality).
const (
	QualityAuto    = "auto"    // full size when paused, limited to MaxPreviewH while playing
	QualityFull    = "full"    // always the full widget resolution (up to the project size)
	QualityHalf    = "half"    // half the widget resolution
	QualityQuarter = "quarter" // a quarter of the widget resolution
)

func validQuality(q string) bool {
	switch q {
	case QualityAuto, QualityFull, QualityHalf, QualityQuarter:
		return true
	}
	return false
}

// Quality returns the effective preview quality.
func (v *Viewer) Quality() string {
	if v.override != "" {
		return v.override
	}
	if validQuality(v.quality) {
		return v.quality
	}
	return QualityAuto
}

// SetQuality overrides the preview quality of this viewer; "" follows the settings.
func (v *Viewer) SetQuality(q string) {
	if q != "" && !validQuality(q) {
		q = ""
	}
	if q == v.override {
		return
	}
	v.override = q
	v.requestRender()
}

// RenderSize returns the pixel size the next frame would be rendered at (0, 0 before
// the first layout).
func (v *Viewer) RenderSize() (int, int) { return v.renderSize() }

// applySettings follows the playback settings (UI thread, from settings.Subscribe).
func (v *Viewer) applySettings(s settings.Settings) {
	if v.closed {
		return
	}
	v.applyCacheBudget(s)
	q, h := s.Playback.PreviewQuality, s.Playback.MaxPreviewH
	if q == v.quality && h == v.maxH {
		return
	}
	v.quality, v.maxH = q, h
	v.requestRender()
}

// ShowSafeArea draws the action-safe (93 %) and title-safe (90 %) guides over the
// picture. The program monitor follows the setting Playback.SafeArea; clean feeds never
// show them.
func (v *Viewer) ShowSafeArea(on bool) {
	if v.safe.obj.Visible() == on {
		return
	}
	if on {
		v.safe.obj.Show()
	} else {
		v.safe.obj.Hide()
	}
	v.pic.Refresh()
}

// SafeAreaShown reports whether the guides are drawn.
func (v *Viewer) SafeAreaShown() bool { return v.safe.obj.Visible() }

// safeArea is the guide overlay: two thin translucent frames and small centre ticks.
type safeArea struct {
	obj            *fyne.Container
	action, title  *canvas.Rectangle
	crossH, crossV *canvas.Rectangle
}

var safeLine = color.NRGBA{0xFF, 0xFF, 0xFF, 0x70}

func newSafeArea() *safeArea {
	frame := func(c color.NRGBA) *canvas.Rectangle {
		r := canvas.NewRectangle(color.Transparent)
		r.StrokeColor = c
		r.StrokeWidth = 1
		return r
	}
	a := &safeArea{
		action: frame(safeLine),
		title:  frame(color.NRGBA{0xFF, 0xFF, 0xFF, 0x48}),
		crossH: canvas.NewRectangle(safeLine),
		crossV: canvas.NewRectangle(safeLine),
	}
	a.obj = container.New(a, a.action, a.title, a.crossH, a.crossV)
	a.obj.Hide()
	return a
}

// Layout places the guides inside the picture area.
func (a *safeArea) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	inset := func(r *canvas.Rectangle, frac float32) {
		dx, dy := size.Width*(1-frac)/2, size.Height*(1-frac)/2
		r.Move(fyne.NewPos(dx, dy))
		r.Resize(fyne.NewSize(size.Width-2*dx, size.Height-2*dy))
	}
	inset(a.action, 0.93)
	inset(a.title, 0.90)
	const arm = 9
	cx, cy := size.Width/2, size.Height/2
	a.crossH.Move(fyne.NewPos(cx-arm, cy))
	a.crossH.Resize(fyne.NewSize(2*arm+1, 1))
	a.crossV.Move(fyne.NewPos(cx, cy-arm))
	a.crossV.Resize(fyne.NewSize(1, 2*arm+1))
}

func (a *safeArea) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }
