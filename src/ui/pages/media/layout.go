package media

import (
	"os/exec"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/layout"
)

// Layout IDs of the media page (layout.Page) and the default arrangement: the file
// browser and the inspector at about 250 and 330 px of a 1440 px window.
const (
	splitFiles     = "files"     // file browser | rest
	splitInspector = "inspector" // pool | inspector
	areaFiles      = "files"
	areaInspector  = "inspector"
	extraKind      = "kind" // pool type filter: "", "video", "image", "audio"

	defaultFilesSplit     = 0.175
	defaultInspectorSplit = 0.72
	minFilesWidth         = 210
	minInspectorWidth     = 280
)

// LayoutState reports the split positions, hidden side panels and the type filter.
func (p *Page) LayoutState() layout.Page {
	if p.outer == nil {
		if p.pending != nil {
			return *p.pending
		}
		return layout.Page{}
	}
	l := layout.Page{Splits: map[string]float64{splitFiles: p.outer.Offset, splitInspector: p.inner.Offset}}
	hidden := map[string]bool{}
	if !p.filesBox.Visible() {
		hidden[areaFiles] = true
	}
	if !p.inspBox.Visible() {
		hidden[areaInspector] = true
	}
	if len(hidden) > 0 {
		l.Hidden = hidden
	}
	if p.kindFilter != "" {
		l.Extra = map[string]string{extraKind: string(p.kindFilter)}
	}
	return l
}

// ApplyLayout restores a LayoutState; missing entries get the defaults.
func (p *Page) ApplyLayout(l layout.Page) {
	if p.poolOnly {
		return
	}
	if p.outer == nil {
		p.pending = &l
		return
	}
	split := func(s interface{ SetOffset(float64) }, id string, def float64) {
		off, ok := l.Splits[id]
		if !ok || off <= 0 || off >= 1 {
			off = def
		}
		s.SetOffset(off)
	}
	split(p.outer, splitFiles, defaultFilesSplit)
	split(p.inner, splitInspector, defaultInspectorSplit)
	p.SetAreaVisible(areaFiles, !l.Hidden[areaFiles])
	p.SetAreaVisible(areaInspector, !l.Hidden[areaInspector])
	p.setKindFilter(model.ClipKind(l.Extra[extraKind]))
}

// SetAreaVisible shows or hides the file browser ("files") or the "inspector"; the pool
// takes their room.
func (p *Page) SetAreaVisible(area string, on bool) {
	if p.outer == nil {
		return
	}
	var o fyne.CanvasObject
	switch area {
	case areaFiles:
		o = p.filesBox
	case areaInspector:
		o = p.inspBox
	default:
		return
	}
	if o.Visible() == on {
		return
	}
	if on {
		o.Show()
	} else {
		o.Hide()
	}
	p.inner.Refresh()
	p.outer.Refresh()
}

// setKindFilter selects the pool's type filter (unknown kinds mean all types).
func (p *Page) setKindFilter(k model.ClipKind) {
	for _, c := range kindChoices {
		if c.kind == k {
			p.kindSel.SetSelected(c.label) // OnChanged refreshes the pool
			return
		}
	}
	p.kindSel.SetSelected(kindChoices[0].label)
}

func openWithSystem(dir string) error {
	cmd := exec.Command("xdg-open", dir)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap the child so it does not linger as a zombie
	return nil
}
