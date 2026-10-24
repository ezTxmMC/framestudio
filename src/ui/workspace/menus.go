package workspace

import (
	"fmt"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
)

func check(on bool) int {
	if on {
		return 2
	}
	return 1
}

func disabled(label string) command.Item {
	return command.Item{Label: label, Enabled: func() bool { return false }}
}

// monitorLabel is "DP-2 · 2560×1440 · Hauptmonitor".
func monitorLabel(mon display.Monitor) string {
	s := mon.Name
	if s == "" {
		s = fmt.Sprintf("%s · %d×%d", mon.ID, mon.W, mon.H)
	}
	if mon.Primary {
		s += " · Hauptmonitor"
	}
	return s
}

// modeLabel names a window mode in German.
func modeLabel(mode string) string {
	switch mode {
	case layout.ModeFullscreen:
		return "Vollbild"
	case layout.ModeMaximized:
		return "Maximiert"
	}
	return "Fenster"
}

// layoutItems lists saved layouts; the current one is checked, a click loads it.
func (m *Manager) layoutItems() []command.Item {
	names := layout.List()
	if len(names) == 0 {
		return []command.Item{disabled("Noch keine Layouts gespeichert")}
	}
	def := settings.Get().Display.Layout
	var items []command.Item
	for _, n := range names {
		name := n
		label := name
		if name == def {
			label += " (Standard)"
		}
		items = append(items, command.Item{Label: label, Checked: check(name == m.name), Run: func() { m.LoadLayout(name) }})
	}
	return items
}

// monitorItems builds one submenu per monitor with the quick assignments.
func (m *Manager) monitorItems() []command.Item {
	ms := m.monitors()
	if len(ms) == 0 {
		return []command.Item{disabled("Keine Monitorinformationen verfügbar")}
	}
	mainMon := m.mainMonitor()
	var items []command.Item
	for _, mon := range ms {
		id := mon.ID
		used := func() bool { return !m.unused[id] && !m.closed }
		fullOn := func(panel string) bool {
			d := m.find(panel)
			return d != nil && d.monitor == id && d.win.FullScreen()
		}
		toggle := func(panel string) func() {
			return func() {
				if fullOn(panel) {
					m.find(panel).removePanel(panel)
					return
				}
				m.Detach(panel, id, true)
			}
		}
		panelOK := func(panel string) func() bool {
			return func() bool {
				_, ok := page.PanelByID(panel)
				return ok && used()
			}
		}
		sub := &command.Menu{Title: monitorLabel(mon), Items: []command.Item{
			{Label: "Programm im Vollbild", Checked: check(fullOn(page.PanelProgram)), Run: toggle(page.PanelProgram), Enabled: panelOK(page.PanelProgram)},
			{Label: "Clean-Feed im Vollbild", Checked: check(fullOn(page.PanelCleanFeed)), Run: toggle(page.PanelCleanFeed), Enabled: panelOK(page.PanelCleanFeed)},
			{Label: "Hauptfenster hierher", Checked: check(mainMon == id), Run: func() { m.moveMain(id) },
				Enabled: func() bool { return used() && m.canMoveMain() }},
			{Separator: true},
			{Label: "Nicht verwenden", Checked: check(m.unused[id]), Run: func() { m.setUnused(id, !m.unused[id]) },
				Enabled: func() bool { return len(m.monitors()) > 1 || m.unused[id] }},
		}}
		items = append(items, command.Item{Label: monitorLabel(mon), Submenu: sub})
	}
	return items
}

// canMoveMain reports whether the main window can be put on a chosen monitor: always on
// X11, under Wayland only while it is fullscreen (maximizing cannot pick an output).
func (m *Manager) canMoveMain() bool {
	w := m.host.MainWindow()
	return w != nil && (m.disp.CanPosition() || w.FullScreen())
}

// moveMain puts the main window on monitor id (keeps fullscreen, otherwise maximized).
func (m *Manager) moveMain(id string) {
	w := m.host.MainWindow()
	if w == nil || !m.canMoveMain() {
		return
	}
	mode := layout.ModeMaximized
	if w.FullScreen() {
		mode = layout.ModeFullscreen
	}
	m.place(w, id, mode, layout.Rect{})
	if m.grouping() {
		m.monitorsChanged()
	}
	m.refresh()
}

// windowItems lists the detached windows with focus, fullscreen and close.
func (m *Manager) windowItems() []command.Item {
	if len(m.wins) == 0 {
		return []command.Item{disabled("Keine gelösten Fenster")}
	}
	var items []command.Item
	for _, d := range m.wins {
		d := d
		label := d.info.Title
		if len(d.members) > 0 {
			label += fmt.Sprintf(" + %d Panels", len(d.members))
		}
		if mon, ok := m.monitor(d.monitor); ok {
			label += " — " + mon.ID
		}
		if d.win.FullScreen() {
			label += " (Vollbild)"
		}
		items = append(items, command.Item{Label: label, Submenu: &command.Menu{Title: d.info.Title, Items: []command.Item{
			{Label: "In den Vordergrund", Run: func() { d.win.Show(); d.win.RequestFocus() }},
			{Label: "Vollbild", Checked: check(d.win.FullScreen()), Run: d.toggleFullscreen},
			{Separator: true},
			{Label: "Zurück ins Hauptfenster", Run: d.close},
		}}})
	}
	return items
}

// panelPlaceItems offers "<Panel> auf Monitor ▸" for every registered panel.
func (m *Manager) panelPlaceItems() []command.Item {
	ms := m.monitors()
	var items []command.Item
	for _, p := range page.Panels() {
		id := p.ID
		full := id == page.PanelProgram || id == page.PanelCleanFeed
		var sub []command.Item
		for _, mon := range ms {
			mid := mon.ID
			label := monitorLabel(mon)
			if full {
				label += " (Vollbild)"
			}
			sub = append(sub, command.Item{Label: label,
				Checked: check(m.find(id) != nil && m.find(id).monitor == mid),
				Enabled: func() bool { return !m.unused[mid] && !m.closed },
				Run: func() {
					if full {
						m.Detach(id, mid, true)
					} else {
						m.open(id, mid, layout.ModeMaximized, layout.Rect{}, "")
					}
				}})
		}
		if len(sub) == 0 {
			sub = append(sub, command.Item{Label: "Als Fenster", Run: func() { m.Detach(id, "", false) }})
		}
		items = append(items, command.Item{Label: p.Title + " auf Monitor", Submenu: &command.Menu{Title: p.Title, Items: sub}})
	}
	return items
}
