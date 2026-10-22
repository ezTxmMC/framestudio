// Package shell is the application frame: project manager, project tabs (multiple open
// projects), page bar, menus, shortcuts, saving and autosave.
//
// OWNER: shell agent. Run is the contract; do not change its signature.
package shell

import "fyne.io/fyne/v2"

// Run builds the main window for app a, optionally opening the given project files, and blocks.
func Run(a fyne.App, files []string) {
	w := a.NewWindow("Frame Studio")
	w.ShowAndRun()
}
