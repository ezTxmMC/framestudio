// Package menubar is the custom in-window menu bar (replacing Fyne's native main menu):
// graphite bar with dropdowns, submenus, check marks, shortcuts and keyboard navigation,
// built from command.MenuBar and the registry.
//
// OWNER: menubar agent. Exported signatures in this file are a contract.
package menubar

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/command"
)

type MenuBar struct {
	widget.BaseWidget
	reg   *command.Registry
	menus []command.Menu
	win   fyne.Window
}

// New builds the bar for window w. Leading/trailing objects (logo, tabs, status) are
// placed by the shell around it.
func New(w fyne.Window, reg *command.Registry, menus []command.Menu) *MenuBar {
	m := &MenuBar{reg: reg, menus: menus, win: w}
	m.ExtendBaseWidget(m)
	return m
}

// Open opens the menu at index i (Alt+letter / F10 navigation).
func (m *MenuBar) Open(i int) {}

func (m *MenuBar) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(widget.NewLabel("Menü"))
}

// ShowPalette opens the command palette (fuzzy search over available commands).
func ShowPalette(w fyne.Window, reg *command.Registry) {}
