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
	closed  bool
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
	// thumbs holds strip posters by thumbKey (media ID, path and source time), so a
	// relinked or trimmed clip gets a fresh frame.
	thumbs     map[string]image.Image
	thumbState map[string]thumbState

	// autoID is the clip the page selected by itself (nothing was selected and the
	// playhead was on it). While it stays the only selection, playback moves the
	// selection along with the playhead. Any other selection change - a click in the
	// strip, Up/Down, a selection made on another page - clears it, and the selection
	// then stays put until the user changes it.
	autoID string

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
	if p.viewer != nil {
		p.viewer.SetActive(true)
	}
	p.autoSelect(true)
	p.refreshStrip()
	p.primary.refresh()
	if f := p.scopes.latest(); f != nil {
		p.scopes.submit(f, p.scope)
	}
}

// Deactivate stops the hidden viewer from rendering every seek and playback tick.
func (p *Page) Deactivate() {
	p.active = false
	if p.viewer != nil {
		p.viewer.SetActive(false)
	}
}

func (p *Page) Close() {
	p.closed = true
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
	p.autoID = ""
	p.s.SelectClip(c.ID)
	p.s.Seek(c.Start)
	p.scrollToSelected()
}

// clickCard handles a click on strip card i: plain click picks the clip and seeks to
// it, Ctrl+click adds or removes it from the selection, Shift+click selects the range
// from the graded clip to it. Modified clicks leave the playhead alone.
func (p *Page) clickCard(i int, mods fyne.KeyModifier) {
	if i < 0 || i >= len(p.entries) {
		return
	}
	id := p.entries[i].Clip.ID
	switch {
	case mods&(fyne.KeyModifierControl|fyne.KeyModifierSuper) != 0:
		p.autoID = ""
		p.s.ToggleClip(id)
	case mods&fyne.KeyModifierShift != 0:
		anchor := -1
		for j, e := range p.entries {
			if e.Clip.ID == p.s.SelectedClip {
				anchor = j
			}
		}
		if anchor < 0 {
			p.pick(p.entries[i].Clip)
			return
		}
		step := 1
		if i < anchor {
			step = -1
		}
		var ids []string
		for j := anchor; ; j += step {
			ids = append(ids, p.entries[j].Clip.ID)
			if j == i {
				break
			}
		}
		p.autoID = ""
		p.s.SelectClips(ids) // the clicked clip is last, so it becomes the graded one
	default:
		p.pick(p.entries[i].Clip)
	}
}

func (p *Page) changed(c session.Change) {
	if c&(session.ChangeProject|session.ChangeSelection|session.ChangePlayhead) == 0 {
		return
	}
	if p.autoID != "" && (p.s.SelectedClip != p.autoID || len(p.s.Selected) != 1) {
		p.autoID = "" // the user (or another page) changed the selection
	}
	if p.autoSelect(c&session.ChangePlayhead != 0) {
		return // SelectClip notified and refreshed already
	}
	if c&(session.ChangeProject|session.ChangeSelection) != 0 {
		p.refreshStrip()
		p.primary.refresh()
	}
}

// autoSelect keeps a picture clip selected while the page is visible:
//   - nothing selected: select the top-most visible clip under the playhead;
//   - with replace (page opens, playhead moved), a selection that cannot be graded
//     (e.g. an audio clip picked on the edit page) is replaced the same way;
//   - during playback, a clip the page selected by itself follows the playhead.
//
// A selection the user made is never changed. It reports whether the selection changed.
func (p *Page) autoSelect(replace bool) bool {
	if !p.active {
		return false
	}
	following := p.autoID != "" && p.s.Playing && replace
	if !following {
		if sel, ok := p.s.Clip(); ok && (sel.Kind.IsVisual() || !replace) {
			return false
		}
	}
	c := clipUnder(p.s.Project, p.s.Playhead)
	if c == nil || c.ID == p.s.SelectedClip {
		return false
	}
	p.autoID = c.ID
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
		i := len(p.cards)
		p.cards = append(p.cards, newClipCard(func(mods fyne.KeyModifier) { p.clickCard(i, mods) }))
	}
	objs := make([]fyne.CanvasObject, len(p.entries))
	var loads []func()
	used := map[string]bool{}
	for i, e := range p.entries {
		thumb, missing, load := p.thumbFor(e.Clip, used)
		if load != nil {
			loads = append(loads, load)
		}
		p.cards[i].set(i+1, e, pr.Settings.FPS, thumb, missing)
		p.cards[i].setSelected(p.s.IsSelected(e.Clip.ID), e.Clip.ID == p.s.SelectedClip)
		objs[i] = p.cards[i]
	}
	for k := range p.thumbs { // drop posters of relinked, trimmed or deleted clips
		if !used[k] {
			delete(p.thumbs, k)
		}
	}
	for k, st := range p.thumbState {
		if !used[k] && st != thumbLoading {
			delete(p.thumbState, k)
		}
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

// posterSec is the source time of the clip's first frame, the strip's representative
// picture (0 for stills).
func posterSec(c model.Clip, fps int) float64 {
	if c.Kind != model.KindVideo || fps <= 0 {
		return 0
	}
	return float64(c.SourceIn) / float64(fps)
}

// thumbKey identifies a strip poster: media ID, file path (changes on relink) and the
// source time (changes when the clip is trimmed or slipped).
func thumbKey(m model.MediaItem, sec float64) string {
	return m.ID + "\x00" + m.Path + "\x00" + strconv.FormatFloat(sec, 'f', 3, 64)
}

// thumbFor returns the cached poster of a media clip and marks its key in used. If it
// still has to be loaded, load is the background job that fetches it and refreshes the
// strip.
func (p *Page) thumbFor(c model.Clip, used map[string]bool) (img image.Image, missing bool, load func()) {
	if c.MediaID == "" || c.Kind == model.KindColor || c.Kind == model.KindTitle {
		return nil, false, nil
	}
	m, ok := p.s.Project.MediaByID(c.MediaID)
	if !ok || p.ctx.Media == nil {
		return nil, true, nil
	}
	sec := posterSec(c, p.s.FPS())
	key := thumbKey(*m, sec)
	used[key] = true
	if img, ok := p.thumbs[key]; ok {
		return img, false, nil
	}
	switch p.thumbState[key] {
	case thumbFailed:
		return nil, true, nil
	case thumbLoading:
		return nil, false, nil
	}
	p.thumbState[key] = thumbLoading
	item, cache := *m, p.ctx.Media
	return nil, false, func() {
		img, err := cache.Poster(item, sec, thumbW*2, thumbH*2)
		kit.Do(func() {
			if p.closed {
				return
			}
			if err != nil || img == nil {
				p.thumbState[key] = thumbFailed
			} else {
				p.thumbs[key] = img
				delete(p.thumbState, key)
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
