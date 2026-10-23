package edit

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// fullscreen is the program monitor filling the whole window (view.viewerFullscreen).
// The viewer and its transport move into a window overlay; Esc brings them back.
type fullscreen struct {
	host *fsHost
	c    fyne.Canvas
}

// fsHost holds the moved monitor and takes the keyboard focus, so keys reach the page
// while the overlay covers the window.
type fsHost struct {
	widget.BaseWidget
	p       *Page
	content fyne.CanvasObject
}

func newFSHost(p *Page, monitor fyne.CanvasObject) *fsHost {
	bg := canvas.NewRectangle(studiotheme.MonitorOut)
	hint := canvas.NewText("Esc beendet die Vollbildansicht", studiotheme.Muted)
	hint.TextSize = 11
	chip := canvas.NewRectangle(color.NRGBA{A: 0x90})
	chip.CornerRadius = 3
	label := container.NewStack(chip, container.New(layout.NewCustomPaddedLayout(3, 3, 8, 8), hint))
	top := container.New(layout.NewCustomPaddedLayout(10, 0, 0, 10), container.NewHBox(layout.NewSpacer(), label))
	h := &fsHost{p: p, content: container.NewStack(bg, monitor, container.NewVBox(top))}
	h.ExtendBaseWidget(h)
	return h
}

func (h *fsHost) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(h.content) }

// Tapped takes the focus back after a click on the picture.
func (h *fsHost) Tapped(*fyne.PointEvent) { h.focus() }

func (h *fsHost) focus() {
	if c := fyne.CurrentApp().Driver().CanvasForObject(h); c != nil {
		c.Focus(h)
	}
}

func (h *fsHost) FocusGained()   {}
func (h *fsHost) FocusLost()     {}
func (h *fsHost) TypedRune(rune) {}
func (h *fsHost) TypedKey(k *fyne.KeyEvent) {
	p := h.p
	if k.Name == fyne.KeyEscape {
		p.exitFullscreen()
		return
	}
	if p.TypedKey(k) || p.reg.Dispatch(k.Name, heldModifiers()) {
		return
	}
	if k.Name == fyne.KeySpace { // the shell's play command may not be bound (tests, tools)
		p.ctx.Session.TogglePlaying()
	}
}

// toggleFullscreen switches the program monitor between its panel and the window.
func (p *Page) toggleFullscreen() {
	if p.fs != nil {
		p.exitFullscreen()
	} else {
		p.enterFullscreen()
	}
}

func (p *Page) canFullscreen() bool {
	return p.panel == "" && p.progBody != nil && p.ctx.Window != nil
}

func (p *Page) enterFullscreen() {
	if p.fs != nil || !p.canFullscreen() {
		return
	}
	c := p.ctx.Window.Canvas()
	p.progBody.Remove(p.progBox)
	host := newFSHost(p, p.progBox)
	kit.SetPassiveOverlay(host, true) // command shortcuts (Strg+Z, Strg+S) keep working
	c.Overlays().Add(host)
	host.Resize(c.Size())
	p.fs = &fullscreen{host: host, c: c}
	c.Focus(host)
	p.setViewerActive()
	p.reg.Refresh()
}

func (p *Page) exitFullscreen() {
	fs := p.fs
	if fs == nil {
		return
	}
	p.fs = nil
	fs.c.Overlays().Remove(fs.host)
	kit.SetPassiveOverlay(fs.host, false)
	p.progBody.Add(p.progBox)
	p.progBody.Refresh()
	p.setViewerActive()
	p.reg.Refresh()
}
