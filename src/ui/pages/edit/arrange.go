package edit

import (
	"framestudio-proto/src/ui/page"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/ui/layout"
)

// Areas of the page that can be hidden (layout.Page.Hidden keys).
const (
	areaLibrary   = "library"
	areaViewer    = "viewer"
	areaInspector = "inspector"
	areaTimeline  = "timeline" // never hidden
)

// Splits of the page (layout.Page.Splits keys).
const (
	splitLibrary   = "library"   // library | rest
	splitInspector = "inspector" // centre | inspector
	splitTimeline  = "timeline"  // monitor row over timeline
)

// arrange builds the split tree for the shown areas. With the viewer the page is the
// monitor row (library | program | inspector) over a full-width timeline; without it
// the timeline takes the whole centre column between library and inspector (the
// program can run on another monitor). Split positions carry over.
func (p *Page) arrange() {
	if p.frame == nil {
		return
	}
	p.saveOffsets()
	row := func(center fyne.CanvasObject) fyne.CanvasObject {
		out := center
		p.rsplit, p.hsplit = nil, nil
		if !p.areaHidden(areaInspector) {
			p.rsplit = container.NewHSplit(center, p.areas[areaInspector])
			p.rsplit.Offset = p.offsets[splitInspector]
			out = p.rsplit
		}
		if !p.areaHidden(areaLibrary) {
			p.hsplit = container.NewHSplit(p.areas[areaLibrary], out)
			p.hsplit.Offset = p.offsets[splitLibrary]
			out = p.hsplit
		}
		return out
	}
	p.vsplit = nil
	var tree fyne.CanvasObject
	if p.areaHidden(areaTimeline) {
		center := fyne.CanvasObject(container.NewStack())
		if !p.areaHidden(areaViewer) {
			center = p.areas[areaViewer]
		}
		tree = row(center)
	} else if p.areaHidden(areaViewer) {
		tree = row(p.areas[areaTimeline])
	} else {
		p.vsplit = container.NewVSplit(row(p.areas[areaViewer]), p.areas[areaTimeline])
		p.vsplit.Offset = p.offsets[splitTimeline]
		tree = p.vsplit
	}
	p.frame.Objects = []fyne.CanvasObject{tree}
	p.frame.Refresh()
	p.setViewerActive()
}

// saveOffsets keeps the positions of the shown splits for the next arrangement.
func (p *Page) saveOffsets() {
	for id, sp := range map[string]*container.Split{splitLibrary: p.hsplit, splitInspector: p.rsplit, splitTimeline: p.vsplit} {
		if sp != nil {
			p.offsets[id] = sp.Offset
		}
	}
}

// setHidden shows or hides an area (library, viewer, inspector).
func (p *Page) setHidden(area string, hide bool) {
	if area == areaTimeline || p.hidden[area] == hide {
		return
	}
	if hide {
		p.hidden[area] = true
	} else {
		delete(p.hidden, area)
	}
	p.arrange()
	p.reg.Refresh()
}

// LayoutState returns the hidden areas and the split positions the user has set; the
// other splits keep following the window size.
func (p *Page) LayoutState() layout.Page {
	st := layout.Page{Splits: map[string]float64{}, Hidden: map[string]bool{}}
	for a, h := range p.hidden {
		if h {
			st.Hidden[a] = true
		}
	}
	if p.frame == nil {
		return st
	}
	p.saveOffsets()
	live := map[string]*container.Split{splitLibrary: p.hsplit, splitInspector: p.rsplit, splitTimeline: p.vsplit}
	for id, a := range p.fit.trackers() {
		if sp := live[id]; sp != nil {
			a.check(sp) // a divider dragged since the last layout
		}
		if a.manual {
			st.Splits[id] = p.offsets[id]
		}
	}
	return st
}

// ApplyLayout restores hidden areas and split positions; splits without a saved
// position size themselves to the window again.
func (p *Page) ApplyLayout(l layout.Page) {
	p.hidden = map[string]bool{}
	for _, a := range []string{areaLibrary, areaViewer, areaInspector} {
		if l.Hidden[a] {
			p.hidden[a] = true
		}
	}
	if p.frame == nil {
		p.pending = &l // applied when the content is built
		return
	}
	for id, a := range p.fit.trackers() {
		if v, ok := l.Splits[id]; ok && v > 0 && v < 1 {
			p.offsets[id] = v
			*a = autoOffset{set: v, manual: true}
		} else {
			*a = autoOffset{}
		}
	}
	p.vsplit, p.hsplit, p.rsplit = nil, nil, nil // offsets come from p.offsets
	p.arrange()
	p.reg.Refresh()
}

// tlMaxPart caps the timeline panel's share of the page height; below it the panel is
// sized so every track shows without scrolling (Timeline.ContentHeight).
const tlMaxPart = 0.55

// Preferred panel widths: the library and inspector keep a usable width, the program
// monitor takes the rest.
const (
	libFrac, libMinW, libMaxW    = 0.2, 240, 300
	inspFrac, inspMinW, inspMaxW = 0.25, 300, 380
)

// fitTimeline sizes the page splits for the window: the timeline panel shows all tracks
// and library and inspector get a usable width. Each split follows the window until the
// user drags its divider.
type fitTimeline struct {
	pg              *Page
	vert, lib, insp autoOffset
}

func (l *fitTimeline) trackers() map[string]*autoOffset {
	return map[string]*autoOffset{splitTimeline: &l.vert, splitLibrary: &l.lib, splitInspector: &l.insp}
}

// autoOffset sets a split offset until the user moves the divider.
type autoOffset struct {
	set    float64 // offset last chosen here; a different value means the user dragged
	manual bool
}

// check notes a divider the user moved.
func (a *autoOffset) check(sp *container.Split) {
	if a.set != 0 && sp.Offset != a.set {
		a.manual = true
	}
}

func (a *autoOffset) apply(sp *container.Split, off float64) {
	a.check(sp)
	if a.manual {
		return
	}
	off = max(0.05, min(0.95, off))
	if off != sp.Offset {
		sp.Offset = off
		sp.Refresh()
	}
	a.set = sp.Offset
}

// splitGap returns the thickness of a split's divider, or the theme padding before the
// first layout.
func splitGap(sp *container.Split, total float32, horizontal bool) float32 {
	a, b := sp.Leading.Size(), sp.Trailing.Size()
	d := total - a.Height - b.Height
	if horizontal {
		d = total - a.Width - b.Width
	}
	if a.IsZero() || d < 0 || d > 40 {
		return theme.Padding()
	}
	return d
}

func (l *fitTimeline) wantHeight() float32 {
	h := l.pg.tl.ContentHeight() + 4
	bottom := l.pg.areas[areaTimeline]
	// panel chrome = panel min height minus the timeline's own min height
	chrome := bottom.MinSize().Height - l.pg.timelineHost.MinSize().Height
	return h + chrome
}

func (l *fitTimeline) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.Position{})
		o.Resize(size)
	}
	if size.Height < 1 || size.Width < 1 {
		return
	}
	pg := l.pg
	if pg.vsplit != nil {
		avail := float64(size.Height - splitGap(pg.vsplit, size.Height, false))
		want := float64(min(l.wantHeight(), size.Height*tlMaxPart))
		l.vert.apply(pg.vsplit, 1-want/avail)
	}

	w := float64(size.Width)
	libW := 0.0
	if pg.hsplit != nil {
		libW = max(libMinW, min(libMaxW, libFrac*w))
		l.lib.apply(pg.hsplit, libW/(w-float64(splitGap(pg.hsplit, size.Width, true))))
	}
	if pg.rsplit != nil {
		rest := float64(pg.rsplit.Size().Width)
		if rest < 1 || pg.hsplit == nil {
			rest = w - libW
		}
		inspW := max(inspMinW, min(inspMaxW, inspFrac*w))
		l.insp.apply(pg.rsplit, 1-inspW/(rest-float64(splitGap(pg.rsplit, float32(rest), true))))
	}
}

func (l *fitTimeline) MinSize(objs []fyne.CanvasObject) fyne.Size {
	if len(objs) == 0 {
		return fyne.Size{}
	}
	return objs[0].MinSize()
}

func (p *Page) areaHidden(area string) bool { return p.hidden[area] || p.docked[area] }

// SetDockedPanels removes the original view while its panel lives in a dock.
// Stored user visibility stays separate, so returning a panel restores the page.
func (p *Page) SetDockedPanels(ids []string) {
	next := map[string]bool{}
	for _, id := range ids {
		switch id {
		case page.PanelProgram:
			next[areaViewer] = true
		case page.PanelTimeline:
			next[areaTimeline] = true
		case page.PanelInspector:
			next[areaInspector] = true
		case page.PanelLibrary:
			next[areaLibrary] = true
		}
	}
	changed := len(next) != len(p.docked)
	for id := range next {
		if !p.docked[id] {
			changed = true
		}
	}
	p.docked = next
	if changed {
		p.arrange()
	}
}
