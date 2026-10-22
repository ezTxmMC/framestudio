// Package color is the "color" workspace page.
//
// OWNER: color page agent. New(ctx) is the contract; do not change its signature.
package color

import (
	"image"
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/viewer"
)

// Page is the Resolve-style colour page: viewer and scopes on top, the clip strip in the
// middle and the primary grading controls at the bottom.
type Page struct {
	ctx *page.Context
	s   *session.Session

	// async runs background work; tests replace it with a synchronous call.
	async func(func())

	content fyne.CanvasObject
	active  bool
	unsub   func()

	viewer       *viewer.Viewer
	compare      bool
	compareBtn   *widget.Button
	compareBadge fyne.CanvasObject

	scope        scopeKind
	scopeBtns    []*widget.Button
	scopeImg     *canvas.Image
	scopeWaiting fyne.CanvasObject
	scopeScale   *fyne.Container
	scopes       *scopeRunner

	entries     []stripClip
	cards       []*clipCard
	stripBox    *fyne.Container
	stripScroll *container.Scroll
	stripEmpty  fyne.CanvasObject
	stripPanel  fyne.CanvasObject
	stripCount  *canvas.Text
	thumbs      map[string]image.Image
	thumbState  map[string]thumbState

	primary *primaryPanel
}

type thumbState int

const (
	thumbLoading thumbState = iota + 1
	thumbFailed
)

func New(ctx *page.Context) page.Page {
	return &Page{
		ctx: ctx, s: ctx.Session,
		async:      func(f func()) { go f() },
		thumbs:     map[string]image.Image{},
		thumbState: map[string]thumbState{},
	}
}

func (p *Page) Content() fyne.CanvasObject {
	if p.content != nil {
		return p.content
	}
	p.scopes = &scopeRunner{async: func(f func()) { p.async(f) }, show: p.showScope}

	top := container.NewHSplit(p.buildViewer(), p.buildScopes())
	top.Offset = 0.6
	p.primary = newPrimaryPanel(p)
	bottom := container.New(layout.NewCustomPaddedVBoxLayout(4), p.buildStrip(), p.primary.object)
	body := container.New(layout.NewCustomPaddedLayout(4, 4, 4, 4),
		container.NewBorder(nil, bottom, nil, nil, top))
	p.content = container.NewStack(canvas.NewRectangle(studiotheme.Gap), body)

	p.unsub = p.s.Subscribe(p.changed)
	p.refreshStrip()
	p.primary.refresh()
	return p.content
}

func (p *Page) buildViewer() fyne.CanvasObject {
	p.viewer = viewer.New(p.s, p.ctx.Renderer)
	p.viewer.OnFrame = p.onFrame
	p.compareBtn = widget.NewButton("Vorher/Nachher", p.toggleCompare)
	p.compareBtn.Importance = widget.LowImportance

	bg := canvas.NewRectangle(color.NRGBA{0x0B, 0x0B, 0x0D, 0xFF})
	bg.CornerRadius = 4
	badgeText := canvas.NewText("VORHER", studiotheme.Text)
	badgeText.TextSize = 11
	badgeText.TextStyle = fyne.TextStyle{Bold: true}
	badgeBG := canvas.NewRectangle(color.NRGBA{0x10, 0x11, 0x14, 0xCC})
	badgeBG.CornerRadius = 3
	badgeBG.StrokeColor = studiotheme.Steel
	badgeBG.StrokeWidth = 1
	badge := container.NewStack(badgeBG, container.New(layout.NewCustomPaddedLayout(2, 2, 8, 8), badgeText))
	p.compareBadge = container.New(layout.NewCustomPaddedLayout(8, 0, 8, 0),
		container.NewVBox(container.NewHBox(badge)))
	p.compareBadge.Hide()

	monitor := container.NewStack(bg, p.viewer, p.compareBadge)
	return kit.Panel("Viewer", p.compareBtn,
		container.NewBorder(nil, viewer.Transport(p.s), nil, nil, monitor))
}

func (p *Page) buildScopes() fyne.CanvasObject {
	seg := container.NewHBox()
	for i, name := range scopeNames {
		k := scopeKind(i)
		b := widget.NewButton(name, func() { p.setScope(k) })
		p.scopeBtns = append(p.scopeBtns, b)
		seg.Add(b)
	}
	p.updateScopeButtons()

	p.scopeImg = canvas.NewImageFromImage(nil)
	p.scopeImg.FillMode = canvas.ImageFillContain
	p.scopeImg.ScaleMode = canvas.ImageScaleSmooth
	p.scopeImg.Hide()
	bg := canvas.NewRectangle(scopeBG)
	bg.CornerRadius = 4
	waiting := canvas.NewText("Kein Bild – Playhead auf einen Clip setzen", studiotheme.Faint)
	waiting.TextSize = 12
	p.scopeWaiting = container.NewCenter(waiting)
	p.scopeScale = container.New(waveScaleLayout{})
	for _, l := range []string{"100", "75", "50", "25", "0"} {
		t := kit.MonoText(l, studiotheme.Faint, 9)
		p.scopeScale.Add(t)
	}
	p.scopeScale.Hide()
	return kit.Panel("Scopes", seg, container.NewStack(bg,
		container.New(layout.NewCustomPaddedLayout(6, 6, 6, 6), p.scopeImg, p.scopeScale), p.scopeWaiting))
}

func (p *Page) buildStrip() fyne.CanvasObject {
	p.stripBox = container.New(layout.NewCustomPaddedHBoxLayout(6))
	p.stripScroll = container.NewHScroll(p.stripBox)
	probe := newClipCard(nil)
	p.stripScroll.SetMinSize(fyne.NewSize(0, probe.MinSize().Height+10))
	msg := canvas.NewText("Keine Bildclips in der Timeline. Lege auf der Seite „Schnitt“ Clips an, um sie hier zu graden.", studiotheme.Muted)
	msg.TextSize = 12
	p.stripEmpty = container.NewCenter(msg)
	p.stripCount = kit.MonoText("", studiotheme.Muted, 11)
	p.stripPanel = kit.Panel("Clips", container.NewPadded(p.stripCount), container.NewStack(p.stripScroll, p.stripEmpty))
	return p.stripPanel
}

func (p *Page) Activate() {
	p.active = true
	if p.content == nil {
		return
	}
	p.autoSelect()
	p.refreshStrip()
	p.primary.refresh()
	if f := p.scopes.latest(); f != nil {
		p.scopes.submit(f, p.scope)
	}
}

func (p *Page) Deactivate() { p.active = false }

func (p *Page) Close() {
	if p.unsub != nil {
		p.unsub()
		p.unsub = nil
	}
	if p.scopes != nil {
		p.scopes.close()
	}
	if p.viewer != nil {
		p.viewer.Close()
	}
}

// TypedKey: Left/Right step one frame, Up/Down jump to the previous/next clip, Space plays.
func (p *Page) TypedKey(k *fyne.KeyEvent) bool {
	switch k.Name {
	case fyne.KeyLeft:
		p.s.Seek(p.s.Playhead - 1)
	case fyne.KeyRight:
		p.s.Seek(p.s.Playhead + 1)
	case fyne.KeyUp:
		p.stepClip(-1)
	case fyne.KeyDown:
		p.stepClip(1)
	case fyne.KeySpace:
		p.s.TogglePlaying()
	default:
		return false
	}
	return true
}

// stepClip selects the clip d positions away in the strip and moves the playhead to it.
func (p *Page) stepClip(d int) {
	entries := visualClips(p.s.Project)
	if len(entries) == 0 {
		return
	}
	cur := -1
	for i, e := range entries {
		if e.Clip.ID == p.s.SelectedClip {
			cur = i
		}
	}
	if cur < 0 {
		if c := clipUnder(p.s.Project, p.s.Playhead); c != nil {
			for i, e := range entries {
				if e.Clip.ID == c.ID {
					cur = i
				}
			}
		}
	}
	next := cur + d
	if cur < 0 {
		next = 0
		if d < 0 {
			next = len(entries) - 1
		}
	}
	if next < 0 || next >= len(entries) {
		return
	}
	p.pick(entries[next].Clip)
}

// pick selects clip c and seeks to its first frame.
func (p *Page) pick(c model.Clip) {
	p.s.SelectClip(c.ID)
	p.s.Seek(c.Start)
	p.scrollToSelected()
}

func (p *Page) changed(c session.Change) {
	if c&(session.ChangeProject|session.ChangeSelection|session.ChangePlayhead) == 0 {
		return
	}
	if p.autoSelect() {
		return // SelectClip notified and refreshed already
	}
	if c&(session.ChangeProject|session.ChangeSelection) != 0 {
		p.refreshStrip()
		p.primary.refresh()
	}
}

// autoSelect selects the clip under the playhead when nothing is selected and the page
// is visible. It reports whether the selection changed.
func (p *Page) autoSelect() bool {
	if !p.active || p.s.SelectedClip != "" {
		return false
	}
	c := clipUnder(p.s.Project, p.s.Playhead)
	if c == nil {
		return false
	}
	p.s.SelectClip(c.ID)
	p.scrollToSelected()
	return true
}

func (p *Page) refreshStrip() {
	if p.stripBox == nil {
		return
	}
	pr := p.s.Project
	p.entries = visualClips(pr)
	for len(p.cards) < len(p.entries) {
		card := newClipCard(nil)
		i := len(p.cards)
		card.OnTapped = func() {
			if i < len(p.entries) {
				p.pick(p.entries[i].Clip)
			}
		}
		p.cards = append(p.cards, card)
	}
	objs := make([]fyne.CanvasObject, len(p.entries))
	var loads []func()
	for i, e := range p.entries {
		thumb, missing, load := p.thumbFor(e.Clip)
		if load != nil {
			loads = append(loads, load)
		}
		p.cards[i].set(i+1, e, pr.Settings.FPS, thumb, missing)
		p.cards[i].setSelected(e.Clip.ID == p.s.SelectedClip)
		objs[i] = p.cards[i]
	}
	if len(objs) != len(p.stripBox.Objects) {
		p.stripBox.Objects = objs
		p.stripBox.Refresh()
	}
	if len(p.entries) > 0 {
		p.stripEmpty.Hide()
	} else {
		p.stripEmpty.Show()
	}
	if n := clipCount(len(p.entries)); n != p.stripCount.Text {
		p.stripCount.Text = n
		p.stripPanel.Refresh()
	}
	for _, load := range loads {
		p.async(load)
	}
}

func clipCount(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 Clip"
	}
	return strconv.Itoa(n) + " Clips"
}

// thumbFor returns the cached poster of a media clip. If it still has to be loaded,
// load is the background job that fetches it and refreshes the strip.
func (p *Page) thumbFor(c model.Clip) (img image.Image, missing bool, load func()) {
	if c.MediaID == "" || c.Kind == model.KindColor || c.Kind == model.KindTitle {
		return nil, false, nil
	}
	m, ok := p.s.Project.MediaByID(c.MediaID)
	if !ok || p.ctx.Media == nil {
		return nil, true, nil
	}
	if img, ok := p.thumbs[m.ID]; ok {
		return img, false, nil
	}
	switch p.thumbState[m.ID] {
	case thumbFailed:
		return nil, true, nil
	case thumbLoading:
		return nil, false, nil
	}
	p.thumbState[m.ID] = thumbLoading
	item, cache := *m, p.ctx.Media
	return nil, false, func() {
		img, err := cache.Thumbnail(item, thumbW*2, thumbH*2)
		kit.Do(func() {
			if err != nil || img == nil {
				p.thumbState[item.ID] = thumbFailed
			} else {
				p.thumbs[item.ID] = img
				delete(p.thumbState, item.ID)
			}
			p.refreshStrip()
		})
	}
}

// scrollToSelected scrolls the strip so the selected card is visible.
func (p *Page) scrollToSelected() {
	if p.stripScroll == nil {
		return
	}
	for i, e := range p.entries {
		if e.Clip.ID != p.s.SelectedClip {
			continue
		}
		x := float32(i) * (cardW + 6)
		off := p.stripScroll.Offset
		w := p.stripScroll.Size().Width
		switch {
		case x < off.X:
			off.X = x
		case x+cardW > off.X+w:
			off.X = x + cardW - w
		default:
			return
		}
		p.stripScroll.ScrollToOffset(off)
		return
	}
}

func (p *Page) toggleCompare() {
	p.compare = !p.compare
	p.viewer.SetCompare(p.compare)
	if p.compare {
		p.compareBtn.Importance = widget.HighImportance
		p.compareBadge.Show()
	} else {
		p.compareBtn.Importance = widget.LowImportance
		p.compareBadge.Hide()
	}
	p.compareBtn.Refresh()
	p.primary.refresh()
}

func (p *Page) setScope(k scopeKind) {
	if p.scope == k {
		return
	}
	p.scope = k
	p.updateScopeButtons()
	p.scopeImg.Image = nil
	p.scopeImg.Hide()
	p.scopeScale.Hide()
	if f := p.scopes.latest(); f != nil {
		p.scopes.submit(f, k)
	}
}

func (p *Page) updateScopeButtons() {
	for i, b := range p.scopeBtns {
		if scopeKind(i) == p.scope {
			b.Importance = widget.HighImportance
		} else {
			b.Importance = widget.LowImportance
		}
		b.Refresh()
	}
}

// onFrame receives every preview frame from the viewer on the UI thread.
func (p *Page) onFrame(img *image.NRGBA) {
	if !p.active {
		return
	}
	p.scopes.submit(img, p.scope)
}

func (p *Page) showScope(k scopeKind, img *image.NRGBA) {
	if k != p.scope {
		return
	}
	p.scopeImg.Image = img
	p.scopeImg.Show()
	if k == scopeWaveform {
		p.scopeScale.Show()
	} else {
		p.scopeScale.Hide()
	}
	p.scopeImg.Refresh()
	p.scopeWaiting.Hide()
}

func (p *Page) showErr(err error) {
	if p.ctx.ShowErr != nil {
		p.ctx.ShowErr(err)
	}
}

// waveScaleLayout places the IRE labels (100 to 0) next to the waveform graticule lines
// of the contain-fitted scope image.
type waveScaleLayout struct{}

func (waveScaleLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w, h := size.Width, size.Width*scopeH/scopeW
	if h > size.Height {
		h = size.Height
		w = h * scopeW / scopeH
	}
	x0, y0 := (size.Width-w)/2, (size.Height-h)/2
	for i, o := range objs {
		ms := o.MinSize()
		row := float32(waveY(1-float64(i)/float64(len(objs)-1), scopeH)) / scopeH
		o.Resize(ms)
		o.Move(fyne.NewPos(x0+3, y0+row*h-ms.Height-1))
	}
}

func (waveScaleLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }
