// Package settingsui is the preferences window: categories on the left (Allgemein,
// Wiedergabe, Audio, Timeline, Medien & Cache, Export, Darstellung, Monitore,
// Tastenkürzel), live-applied controls on the right.
//
// OWNER: settings agent. Exported signatures in this file are a contract.
package settingsui

import (
	"fyne.io/fyne/v2"

	"framestudio-proto/src/ui/command"
)

// Show opens the settings window (category optional: "audio", "keymap", ...).
func Show(a fyne.App, parent fyne.Window, reg *command.Registry, category string) {}
