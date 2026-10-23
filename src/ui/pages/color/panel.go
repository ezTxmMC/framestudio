package color

import (
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

func init() {
	page.RegisterPanel(page.PanelInfo{
		ID: page.PanelScopes, Title: "Scopes", Icon: page.LazyIcon(theme.ColorChromaticIcon),
		New: func(ctx *page.Context) page.Panel { return newScopesPanel(ctx) },
	})
}

// panelScope is the scope the last scopes panel showed; a new panel starts with it.
var panelScope = scopeWaveform

// scopesPanel shows the scopes of the current playhead frame in its own window. It
// renders its frames itself (frameSource), so it works on every page, also while the
// colour page is hidden or was never opened.
type scopesPanel struct {
	s       *session.Session
	view    *scopeView
	src     *frameSource
	tc      *canvas.Text
	unsub   func()
	content fyne.CanvasObject
	closed  bool
}

func newScopesPanel(ctx *page.Context) *scopesPanel {
	sp := &scopesPanel{s: ctx.Session}
	sp.tc = kit.MonoText("", studiotheme.Muted, 11)
	sp.view = newScopeView(func(f func()) { go f() }, container.NewPadded(container.NewCenter(sp.tc)))
	sp.view.setKind(panelScope)
	sp.view.onKind = func(k scopeKind) { panelScope = k }
	sp.src = newFrameSource(ctx.Session, ctx.Renderer, sp.frame)
	sp.unsub = ctx.Session.Subscribe(func(c session.Change) {
		if c&(session.ChangePlayhead|session.ChangeProject) != 0 {
			sp.updateTC()
		}
	})
	sp.content = container.NewStack(canvas.NewRectangle(studiotheme.Gap), container.NewPadded(sp.view.object))
	sp.updateTC()
	sp.src.request()
	return sp
}

func (sp *scopesPanel) frame(img *image.NRGBA) {
	if !sp.closed {
		sp.view.submit(img)
	}
}

func (sp *scopesPanel) updateTC() {
	if tc := sp.s.Project.Timecode(sp.s.Playhead); tc != sp.tc.Text {
		sp.tc.Text = tc
		sp.tc.Refresh()
	}
}

func (sp *scopesPanel) Content() fyne.CanvasObject { return sp.content }

// TypedKey: 1, 2 and 3 pick histogram, waveform and vectorscope.
func (sp *scopesPanel) TypedKey(k *fyne.KeyEvent) bool {
	switch k.Name {
	case fyne.Key1:
		sp.view.setKind(scopeHistogram)
	case fyne.Key2:
		sp.view.setKind(scopeWaveform)
	case fyne.Key3:
		sp.view.setKind(scopeVectorscope)
	default:
		return false
	}
	return true
}

func (sp *scopesPanel) Close() {
	if sp.closed {
		return
	}
	sp.closed = true
	sp.unsub()
	sp.src.close()
	sp.view.close()
}
