package audio

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynelayout "fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

func init() {
	page.RegisterPanel(page.PanelInfo{
		ID: page.PanelMixer, Title: "Mixer", Icon: page.LazyIcon(theme.VolumeUpIcon),
		New: func(ctx *page.Context) page.Panel { return newMixerPanel(ctx) },
	})
}

// Layout IDs of the audio page (layout.Page).
const (
	splitMain          = "main"   // lanes / bottom row
	splitBottom        = "bottom" // clip | mixer
	areaClip           = "clip"
	areaMixer          = "mixer"
	defaultMainSplit   = 0.5
	defaultBottomSplit = 0.27
)

// mixerPanel is the mixer (channel strips and the metered master) in its own window. It
// is a page in mixer-only mode: same strips, faders and live meters, without lanes.
type mixerPanel struct{ p *Page }

func newMixerPanel(ctx *page.Context) *mixerPanel { return mixerPanelOf(New(ctx).(*Page)) }

// mixerPanelOf turns a fresh page (Content not built yet) into a mixer panel.
func mixerPanelOf(p *Page) *mixerPanel {
	p.mixerOnly = true
	p.Content()
	p.Activate()
	return &mixerPanel{p: p}
}

// mixerContent builds the mixer-only content of the panel.
func (p *Page) mixerContent() fyne.CanvasObject {
	p.mixer = newMixer(p)
	add := widget.NewButtonWithIcon("Spur hinzufügen", theme.ContentAddIcon(), p.addTrack)
	add.Importance = widget.LowImportance
	p.mixerBox = kit.Panel("Mixer", add, p.mixer.obj)
	p.content = container.NewStack(canvas.NewRectangle(studiotheme.Gap),
		container.New(fynelayout.NewCustomPaddedLayout(4, 4, 4, 4), p.mixerBox))
	p.unsub = p.s.Subscribe(p.changed)
	p.refresh()
	return p.content
}

func (mp *mixerPanel) Content() fyne.CanvasObject { return mp.p.content }

// TypedKey: Space plays and stops the session (the panel has no Vorhören).
func (mp *mixerPanel) TypedKey(k *fyne.KeyEvent) bool {
	if k.Name == fyne.KeySpace {
		mp.p.s.TogglePlaying()
		return true
	}
	return false
}

func (mp *mixerPanel) Close() { mp.p.Close() }

// Commands binds audio page commands in the page scope (page.CommandProvider): Space
// (play.toggle) keeps meaning "Vorhören" here, the timeline zoom and "Audiospur
// hinzufügen" act on the audio lanes.
func (p *Page) Commands() []command.Binding {
	p.Content()
	return append(command.AudioBindings(p.s, p.ctx.ShowErr), []command.Binding{
		{ID: "play.toggle", Run: p.togglePreview},
		{ID: "audio.preview", Icon: theme.MediaPlayIcon(), Run: p.togglePreview,
			Checked: func() bool { return p.prev.Busy() },
			Info:    &command.Info{Label: "Vorhören", Category: "Ton"}},
		{ID: "timeline.addAudioTrack", Icon: theme.ContentAddIcon(), Run: p.addTrack},
		{ID: "timeline.zoomIn", Icon: theme.ZoomInIcon(), Run: func() { p.setZoom(p.view.zoom*1.5, -1) }},
		{ID: "timeline.zoomOut", Icon: theme.ZoomOutIcon(), Run: func() { p.setZoom(p.view.zoom/1.5, -1) }},
		{ID: "timeline.zoomFit", Icon: theme.ZoomFitIcon(), Run: func() { p.setZoom(1, -1) }},
	}...)
}

// togglePreview starts Vorhören, or stops it (and playback) when something sounds.
func (p *Page) togglePreview() {
	if p.prev.Busy() || p.s.Playing {
		p.prev.Stop()
		p.s.SetPlaying(false)
		return
	}
	p.prev.Start()
}

// LayoutState reports the split positions and hidden clip or mixer panels.
func (p *Page) LayoutState() layout.Page {
	if p.main == nil {
		if p.pending != nil {
			return *p.pending
		}
		return layout.Page{}
	}
	l := layout.Page{Splits: map[string]float64{splitMain: p.main.Offset, splitBottom: p.bottom.Offset}}
	hidden := map[string]bool{}
	if !p.clipBox.Visible() {
		hidden[areaClip] = true
	}
	if (!p.dockedMixer && !p.mixerBox.Visible()) || (p.dockedMixer && !p.undockedMixerVisible) {
		hidden[areaMixer] = true
	}
	if len(hidden) > 0 {
		l.Hidden = hidden
	}
	return l
}

// ApplyLayout restores a LayoutState; missing entries get the defaults.
func (p *Page) ApplyLayout(l layout.Page) {
	if p.mixerOnly {
		return
	}
	if p.main == nil {
		p.pending = &l
		return
	}
	split := func(s *container.Split, id string, def float64) {
		off, ok := l.Splits[id]
		if !ok || off <= 0 || off >= 1 {
			off = def
		}
		s.SetOffset(off)
	}
	split(p.main, splitMain, defaultMainSplit)
	split(p.bottom, splitBottom, defaultBottomSplit)
	p.SetAreaVisible(areaClip, !l.Hidden[areaClip])
	p.SetAreaVisible(areaMixer, !l.Hidden[areaMixer])
}

// SetAreaVisible shows or hides the "clip" or "mixer" panel of the bottom row (e.g.
// while the mixer is detached into its own window). With both hidden the lanes take
// the whole page.
func (p *Page) SetAreaVisible(area string, on bool) {
	if p.main == nil {
		return
	}
	var o fyne.CanvasObject
	switch area {
	case areaClip:
		o = p.clipBox
	case areaMixer:
		if p.dockedMixer {
			p.undockedMixerVisible = on
			on = false
		}
		o = p.mixerBox
	default:
		return
	}
	if on == o.Visible() {
		return
	}
	if on {
		o.Show()
	} else {
		o.Hide()
	}
	if p.clipBox.Visible() || p.mixerBox.Visible() {
		p.bottom.Show()
	} else {
		p.bottom.Hide()
	}
	p.bottom.Refresh()
	p.main.Refresh()
}

func (p *Page) SetDockedPanels(ids []string) {
	if p.mixerOnly || p.mixerBox == nil {
		return
	}
	want := false
	for _, id := range ids {
		if id == page.PanelMixer {
			want = true
		}
	}
	if want == p.dockedMixer {
		return
	}
	if want {
		p.undockedMixerVisible = p.mixerBox.Visible()
		p.dockedMixer = true
		p.SetAreaVisible(areaMixer, p.undockedMixerVisible)
	} else {
		p.dockedMixer = false
		p.SetAreaVisible(areaMixer, p.undockedMixerVisible)
	}
}
