package display

import (
	"fyne.io/fyne/v2"

	"framestudio-proto/src/ui/osscale"
)

// LiveScale reads what the running app knows about its scale: the window system, the
// canvas scale Fyne ended up with, the texture scale (pixels per canvas pixel) and the
// largest monitor content scale GLFW reports.
func LiveScale(c fyne.Canvas) osscale.Live {
	l := osscale.Live{Platform: Platform(), TexScale: 1}
	if c != nil {
		l.CanvasScale = float64(c.Scale())
		if l.CanvasScale > 0 {
			px, _ := c.PixelCoordinateForPosition(fyne.NewPos(1000, 0))
			l.TexScale = float64(px) / 1000 / l.CanvasScale
		}
	}
	for _, m := range Monitors() {
		l.ContentScale = max(l.ContentScale, float64(m.Scale))
	}
	return l
}
