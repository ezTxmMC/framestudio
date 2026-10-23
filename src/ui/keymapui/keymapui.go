// Package keymapui is the shortcut editor: every command by category with its shortcut,
// search, record-a-shortcut, conflict warnings, reset per command and reset all.
//
// OWNER: menubar agent. Exported signatures in this file are a contract.
package keymapui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/command"
)

// Editor returns the editor content; changes go to reg and are persisted in settings.Keymap.
func Editor(w fyne.Window, reg *command.Registry) fyne.CanvasObject {
	return widget.NewLabel("Tastenkürzel")
}

// Show opens the editor in a dialog.
func Show(w fyne.Window, reg *command.Registry) {}
