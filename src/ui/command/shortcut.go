package command

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

// Shortcut strings use English modifier names joined by "+": "Ctrl+Shift+Z", "Alt+1",
// "Space", "Delete", "F11". Keys are fyne.KeyName values; a few aliases are accepted.

var keyAliases = map[string]fyne.KeyName{
	"space": fyne.KeySpace, "leertaste": fyne.KeySpace,
	"del": fyne.KeyDelete, "entf": fyne.KeyDelete, "delete": fyne.KeyDelete,
	"backspace": fyne.KeyBackspace, "rück": fyne.KeyBackspace,
	"esc": fyne.KeyEscape, "escape": fyne.KeyEscape,
	"enter": fyne.KeyReturn, "return": fyne.KeyReturn,
	"home": fyne.KeyHome, "pos1": fyne.KeyHome, "end": fyne.KeyEnd, "ende": fyne.KeyEnd,
	"left": fyne.KeyLeft, "right": fyne.KeyRight, "up": fyne.KeyUp, "down": fyne.KeyDown,
	"pageup": fyne.KeyPageUp, "pagedown": fyne.KeyPageDown, "tab": fyne.KeyTab,
	"plus": fyne.KeyPlus, "+": fyne.KeyPlus, "minus": fyne.KeyMinus, "-": fyne.KeyMinus,
	",": fyne.KeyComma, "comma": fyne.KeyComma, ".": fyne.KeyPeriod, "period": fyne.KeyPeriod,
	"=": fyne.KeyEqual,
}

var modOrder = []struct {
	name string
	mod  fyne.KeyModifier
}{
	{"Ctrl", fyne.KeyModifierControl},
	{"Alt", fyne.KeyModifierAlt},
	{"Shift", fyne.KeyModifierShift},
	{"Super", fyne.KeyModifierSuper},
}

// Parse splits a shortcut string into key and modifiers. ok is false for invalid input.
func Parse(s string) (key fyne.KeyName, mods fyne.KeyModifier, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, false
	}
	var parts []string
	switch {
	case s == "+":
		parts = []string{"+"}
	case strings.HasSuffix(s, "++"): // "Ctrl++" means Ctrl and the plus key
		parts = append(strings.Split(strings.TrimSuffix(s, "++"), "+"), "+")
	default:
		parts = strings.Split(s, "+")
	}
	for i, p := range parts {
		p = strings.TrimSpace(p)
		low := strings.ToLower(p)
		if i < len(parts)-1 {
			switch low {
			case "ctrl", "strg", "control":
				mods |= fyne.KeyModifierControl
			case "alt":
				mods |= fyne.KeyModifierAlt
			case "shift", "umschalt":
				mods |= fyne.KeyModifierShift
			case "super", "meta", "cmd":
				mods |= fyne.KeyModifierSuper
			default:
				return "", 0, false
			}
			continue
		}
		if k, ok := keyAliases[low]; ok {
			key = k
		} else if len(p) == 1 {
			key = fyne.KeyName(strings.ToUpper(p))
		} else if len(p) <= 3 && (low[0] == 'f') {
			key = fyne.KeyName(strings.ToUpper(p))
		} else {
			key = fyne.KeyName(p)
		}
	}
	return key, mods, key != ""
}

// Format builds the canonical shortcut string for a key and modifiers.
func Format(key fyne.KeyName, mods fyne.KeyModifier) string {
	if key == "" {
		return ""
	}
	var b strings.Builder
	for _, m := range modOrder {
		if mods&m.mod != 0 {
			b.WriteString(m.name)
			b.WriteString("+")
		}
	}
	b.WriteString(string(key))
	return b.String()
}

// Normalize returns the canonical form of a shortcut string, "" if invalid or empty.
func Normalize(s string) string {
	k, m, ok := Parse(s)
	if !ok {
		return ""
	}
	return Format(k, m)
}

var keyLabels = map[fyne.KeyName]string{
	fyne.KeySpace: "Leertaste", fyne.KeyDelete: "Entf", fyne.KeyBackspace: "Rück",
	fyne.KeyEscape: "Esc", fyne.KeyReturn: "Eingabe", fyne.KeyHome: "Pos1", fyne.KeyEnd: "Ende",
	fyne.KeyLeft: "←", fyne.KeyRight: "→", fyne.KeyUp: "↑", fyne.KeyDown: "↓",
	fyne.KeyPageUp: "Bild↑", fyne.KeyPageDown: "Bild↓", fyne.KeyTab: "Tab",
}

// Display renders a shortcut for menus in German: "Strg+Umschalt+Z".
func Display(s string) string {
	k, m, ok := Parse(s)
	if !ok {
		return ""
	}
	var parts []string
	for _, x := range modOrder {
		if m&x.mod != 0 {
			name := map[string]string{"Ctrl": "Strg", "Alt": "Alt", "Shift": "Umschalt", "Super": "Super"}[x.name]
			parts = append(parts, name)
		}
	}
	if l, ok := keyLabels[k]; ok {
		parts = append(parts, l)
	} else {
		parts = append(parts, string(k))
	}
	return strings.Join(parts, "+")
}

// ComboOf returns the key combination a Fyne shortcut stands for, including the desktop
// driver's own shortcut types: Strg+Z/Y/A/C/X/V arrive as fyne.ShortcutUndo and the like,
// Umschalt+Entf as ShortcutCut{Secondary: true}, Umschalt+Einfg as ShortcutPaste and
// Strg+Einfg as ShortcutCopy with Secondary set.
func ComboOf(sc fyne.Shortcut) (fyne.KeyName, fyne.KeyModifier, bool) {
	const ctrl = fyne.KeyModifierShortcutDefault
	switch s := sc.(type) {
	case *desktop.CustomShortcut:
		return s.KeyName, s.Modifier, true
	case *fyne.ShortcutUndo:
		return fyne.KeyZ, ctrl, true
	case *fyne.ShortcutRedo:
		return fyne.KeyY, ctrl, true
	case *fyne.ShortcutSelectAll:
		return fyne.KeyA, ctrl, true
	case *fyne.ShortcutCopy:
		if s.Secondary {
			return fyne.KeyInsert, ctrl, true
		}
		return fyne.KeyC, ctrl, true
	case *fyne.ShortcutCut:
		if s.Secondary {
			return fyne.KeyDelete, fyne.KeyModifierShift, true
		}
		return fyne.KeyX, ctrl, true
	case *fyne.ShortcutPaste:
		if s.Secondary {
			return fyne.KeyInsert, fyne.KeyModifierShift, true
		}
		return fyne.KeyV, ctrl, true
	}
	return "", 0, false
}
