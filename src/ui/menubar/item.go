package menubar

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"framestudio-proto/src/ui/command"
)

// Item is one entry of a context menu (the bar menus use command.Item). A separator has
// only Separator set. Children make the entry a submenu parent (Action is ignored).
type Item struct {
	Label     string
	Shortcut  string // display text, e.g. "Strg+Umschalt+S"
	Icon      fyne.Resource
	Checked   bool // check mark in the icon column
	Radio     bool // with Checked: a dot instead of the check mark
	Disabled  bool
	Separator bool
	Children  []Item
	Action    func()
	Swatch    color.Color // 12x12 colour square in the icon column (clip colours)
}

// Sep is a separator item.
func Sep() Item { return Item{Separator: true} }

// FromFyneMenu converts a Fyne menu to items (labels, shortcuts, icons, check marks,
// disabled state, separators and child menus).
func FromFyneMenu(m *fyne.Menu) []Item {
	if m == nil {
		return nil
	}
	out := make([]Item, 0, len(m.Items))
	for _, mi := range m.Items {
		if mi == nil {
			continue
		}
		if mi.IsSeparator {
			out = append(out, Sep())
			continue
		}
		it := Item{Label: mi.Label, Icon: mi.Icon, Checked: mi.Checked, Disabled: mi.Disabled,
			Action: mi.Action, Shortcut: shortcutOf(mi.Shortcut)}
		if sw, ok := mi.Icon.(*swatchIcon); ok {
			it.Icon, it.Swatch = nil, sw.col
		}
		if mi.ChildMenu != nil {
			it.Children = FromFyneMenu(mi.ChildMenu)
			it.Action = nil
		}
		out = append(out, it)
	}
	return out
}

func shortcutOf(sc fyne.Shortcut) string {
	if sc == nil {
		return ""
	}
	if cs, ok := sc.(*desktop.CustomShortcut); ok {
		return command.Display(command.Format(cs.KeyName, cs.Modifier))
	}
	if k, m, ok := command.ComboOf(sc); ok {
		return command.Display(command.Format(k, m))
	}
	return ""
}

// rowsOf resolves static items to rows.
func rowsOf(items []Item) []row {
	out := make([]row, 0, len(items))
	for _, it := range items {
		if it.Separator {
			out = append(out, row{sep: true})
			continue
		}
		r := row{label: it.Label, shortcut: ShortcutText(it.Shortcut, runtimeGOOS), enabled: !it.Disabled,
			icon: it.Icon, swatch: it.Swatch, run: it.Action}
		if it.Checked {
			r.toggle, r.checked, r.radio = true, true, it.Radio
		}
		if len(it.Children) > 0 {
			kids := it.Children
			r.sub = func() []row { return rowsOf(kids) }
			r.run = nil
			anyOn := false
			for _, k := range kids {
				if !k.Separator && !k.Disabled {
					anyOn = true
				}
			}
			r.enabled = r.enabled && anyOn
		}
		out = append(out, r)
	}
	return tidy(out)
}

// tidy drops leading, trailing and doubled separators.
func tidy(rows []row) []row {
	var out []row
	for _, r := range rows {
		if r.sep && (len(out) == 0 || out[len(out)-1].sep) {
			continue
		}
		out = append(out, r)
	}
	for len(out) > 0 && out[len(out)-1].sep {
		out = out[:len(out)-1]
	}
	return out
}

// swatchIcon is a menu icon that stands for a colour square (see SwatchIcon).
type swatchIcon struct {
	fyne.Resource
	col color.Color
}

// SwatchIcon returns a placeholder icon for fyne.MenuItem.Icon that FromFyneMenu turns
// into a colour swatch (Item.Swatch).
func SwatchIcon(c color.Color) fyne.Resource {
	return &swatchIcon{Resource: fyne.NewStaticResource("swatch", nil), col: c}
}
