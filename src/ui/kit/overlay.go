package kit

import "fyne.io/fyne/v2"

// passive holds window overlays that are part of the workspace rather than a dialog:
// the program monitor in viewer fullscreen. Key routing treats a window whose top
// overlay is passive like a window without overlays, so Strg+Z, Strg+S and the other
// command shortcuts keep working. UI thread only.
var passive = map[fyne.CanvasObject]bool{}

// SetPassiveOverlay marks (or unmarks) o as a passive overlay.
func SetPassiveOverlay(o fyne.CanvasObject, on bool) {
	if on {
		passive[o] = true
	} else {
		delete(passive, o)
	}
}

// IsPassiveOverlay reports whether o was marked with SetPassiveOverlay.
func IsPassiveOverlay(o fyne.CanvasObject) bool { return o != nil && passive[o] }
