package command

import (
	"strings"

	"fyne.io/fyne/v2"
)

// Reserved returns why the key combination sc cannot be given to a command ("" when it
// is free): Alt+letter opens a menu of the menu bar, Alt+1…5 and Strg+1…5 switch pages,
// Strg+Y redoes and F10 puts the keyboard on the menu bar. The shell handles these
// outside the registry; a command shortcut on them would silently disable them.
func Reserved(sc string) string {
	key, mods, ok := Parse(sc)
	if !ok {
		return ""
	}
	switch {
	case mods == fyne.KeyModifierAlt:
		for _, m := range MenuBar {
			if r, ok := mnemonic(m.Title, MenuBar); ok && fyne.KeyName(string(r)) == key {
				return Display(sc) + " öffnet das Menü " + m.Title
			}
		}
		if isPageDigit(key) {
			return Display(sc) + " wechselt die Seite"
		}
	case mods == fyne.KeyModifierShortcutDefault:
		if isPageDigit(key) {
			return Display(sc) + " wechselt die Seite"
		}
		if key == fyne.KeyY {
			return Display(sc) + " wiederholt den letzten Schritt"
		}
	case mods == 0 && key == fyne.KeyF10:
		return "F10 öffnet die Menüleiste"
	}
	return ""
}

func isPageDigit(k fyne.KeyName) bool { return len(k) == 1 && k[0] >= '1' && k[0] <= '5' }

// mnemonic returns the menu letter of title among menus: the first letter of the title
// not taken by an earlier menu (as the menu bar underlines it).
func mnemonic(title string, menus []Menu) (rune, bool) {
	used := map[rune]bool{}
	for _, m := range menus {
		for _, r := range strings.ToUpper(m.Title) {
			if r < 'A' || r > 'Z' || used[r] {
				continue
			}
			used[r] = true
			if m.Title == title {
				return r, true
			}
			break
		}
	}
	return 0, false
}
