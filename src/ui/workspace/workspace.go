// Package workspace manages the windows of the app: detached panel windows on any monitor
// (windowed or fullscreen), layouts (save, restore, presets) and the monitor dialog.
//
// OWNER: workspace agent. Exported signatures in this file are a contract.
package workspace

import (
	"fyne.io/fyne/v2"

	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
)

// Host is what the shell provides to the workspace manager.
type Host interface {
	App() fyne.App
	MainWindow() fyne.Window
	// ActiveContext returns the page context of the active project tab, nil when none.
	ActiveContext() *page.Context
	// OnActiveChange registers fn to run (UI thread) when the active project tab changes.
	OnActiveChange(fn func())
	// PageLayouts returns the layout state of every created page; ApplyPageLayouts restores it.
	PageLayouts() map[string]layout.Page
	ApplyPageLayouts(map[string]layout.Page)
	ActivePage() string
	ShowPage(id string)
	// Commands is the shared registry; detached windows dispatch their keys to it.
	Commands() *command.Registry
}

type Manager struct{ host Host }

func New(h Host) *Manager { return &Manager{host: h} }

// Bindings returns the workspace.* commands and registers the dynamic submenus.
func (m *Manager) Bindings() []command.Binding { return nil }

// Restore applies the layout saved at last quit (or the named layout from settings).
func (m *Manager) Restore() {}

// Current captures the current layout (main window, detached windows, page states).
func (m *Manager) Current() layout.Layout { return layout.Default() }

// Detach opens panel id in its own window (on monitor, fullscreen or not).
func (m *Manager) Detach(panelID, monitorID string, fullscreen bool) {}

// Shutdown saves the layout if enabled and closes all detached windows.
func (m *Manager) Shutdown() {}
