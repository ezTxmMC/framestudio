package color

import (
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
)

// Layout IDs of the colour page (layout.Page).
const (
	splitTop        = "top" // viewer | scopes
	defaultTopSplit = 0.6
	areaScopes      = "scopes" // scopes panel
	areaStrip       = "strip"  // clip strip
	extraScope      = "scope"  // selected scope: histogram, waveform, vectorscope
	scopePrefix     = "scopes."
)

// Commands binds the grade.* and scopes.* commands in the page scope
// (page.CommandProvider). Up and Down (play.prevEdit/nextEdit) step through the clip
// strip while the page is visible, as the strip's own keys do.
func (p *Page) Commands() []command.Binding {
	p.Content()
	graded := func() bool { _, ok := p.primary.target(); return ok }
	unlocked := func() bool { return p.gradeUnlocked() }
	bs := []command.Binding{
		{ID: "grade.reset", Icon: theme.ContentUndoIcon(), Run: p.primary.resetGrade,
			Enabled: func() bool {
				c, ok := p.primary.target()
				return ok && unlocked() && !c.Grade.IsNeutral()
			}},
		{ID: "grade.copy", Icon: theme.ContentCopyIcon(), Run: p.primary.copyGrade, Enabled: graded},
		{ID: "grade.paste", Icon: theme.ContentPasteIcon(), Run: p.primary.pasteGrade,
			Enabled: func() bool { return unlocked() && p.primary.copied != nil }},
		{ID: "grade.applySelection", Run: p.primary.applyToSelection,
			Enabled: func() bool { return graded() && p.primary.selectedVisual() > 1 }},
		{ID: "grade.applyAll", Run: p.primary.applyToAll, Enabled: graded},
		{ID: "grade.compare", Run: p.toggleCompare, Checked: func() bool { return p.compare }},
		{ID: "play.prevEdit", Run: func() { p.stepClip(-1) }},
		{ID: "play.nextEdit", Run: func() { p.stepClip(1) }},
	}
	for i := range scopeNames {
		k := scopeKind(i)
		bs = append(bs, command.Binding{ID: scopePrefix + k.id(),
			Run:     func() { p.setScope(k) },
			Checked: func() bool { return p.sv.kind == k }})
	}
	return bs
}

// gradeUnlocked reports whether the graded clip exists and its track is not locked.
func (p *Page) gradeUnlocked() bool {
	c, ok := p.primary.target()
	if !ok {
		return false
	}
	_, ti, _ := p.s.Project.Clip(c.ID)
	return ti >= 0 && !p.s.Project.Tracks[ti].Locked
}

// refreshCommands tells menus that checked or enabled states changed outside Run.
func (p *Page) refreshCommands() {
	if p.ctx.Commands != nil {
		p.ctx.Commands.Refresh()
	}
}

// LayoutState reports the viewer/scopes split, hidden areas and the selected scope.
func (p *Page) LayoutState() layout.Page {
	if p.content == nil {
		if p.pending != nil {
			return *p.pending
		}
		return layout.Page{}
	}
	l := layout.Page{
		Splits: map[string]float64{splitTop: p.top.Offset},
		Extra:  map[string]string{extraScope: p.sv.kind.id()},
	}
	hidden := map[string]bool{}
	if (!p.dockedScopes && !p.sv.object.Visible()) || (p.dockedScopes && !p.undockedScopesVisible) {
		hidden[areaScopes] = true
	}
	if !p.stripPanel.Visible() {
		hidden[areaStrip] = true
	}
	if len(hidden) > 0 {
		l.Hidden = hidden
	}
	return l
}

// ApplyLayout restores a state from LayoutState; missing entries get the defaults.
func (p *Page) ApplyLayout(l layout.Page) {
	if p.content == nil {
		p.pending = &l
		return
	}
	off, ok := l.Splits[splitTop]
	if !ok || off <= 0 || off >= 1 {
		off = defaultTopSplit
	}
	p.top.SetOffset(off)
	k := scopeHistogram
	if id, ok := l.Extra[extraScope]; ok {
		if pk, ok := parseScope(id); ok {
			k = pk
		}
	}
	p.setScope(k)
	p.SetAreaVisible(areaScopes, !l.Hidden[areaScopes])
	p.SetAreaVisible(areaStrip, !l.Hidden[areaStrip])
}

// SetAreaVisible shows or hides the "scopes" panel (the viewer takes its width, e.g.
// while scopes are detached into their own window) or the clip "strip".
func (p *Page) SetAreaVisible(area string, on bool) {
	if p.content == nil {
		return
	}
	switch area {
	case areaScopes:
		if p.dockedScopes {
			p.undockedScopesVisible = on
			on = false
		}
		setShown(p.sv.object, on)
		p.top.Refresh()
	case areaStrip:
		setShown(p.stripPanel, on)
		p.content.Refresh()
	}
}

func setShown(o interface {
	Visible() bool
	Show()
	Hide()
}, on bool) {
	switch {
	case on && !o.Visible():
		o.Show()
	case !on && o.Visible():
		o.Hide()
	}
}

func (p *Page) SetDockedPanels(ids []string) {
	if p.content == nil || p.sv == nil {
		return
	}
	want := false
	for _, id := range ids {
		if id == page.PanelScopes {
			want = true
		}
	}
	if want == p.dockedScopes {
		return
	}
	if want {
		p.undockedScopesVisible = p.sv.object.Visible()
		p.dockedScopes = true
		p.SetAreaVisible(areaScopes, p.undockedScopesVisible)
	} else {
		p.dockedScopes = false
		p.SetAreaVisible(areaScopes, p.undockedScopesVisible)
	}
}
