package workspace

import (
	"sort"

	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
)

// monPlan is what the monitor dialog edits for one monitor.
type monPlan struct {
	Use    bool
	Mode   string
	Main   bool
	Panels []string // panel IDs, in the order they open
}

// plan maps monitor IDs to their assignment.
type plan map[string]*monPlan

// planFrom describes a layout per monitor. Windows without a known monitor (Wayland)
// count as on the primary monitor; windows waiting for a monitor that is not connected
// are left out (keepWaiting carries them through).
func planFrom(l layout.Layout, ms []display.Monitor, unused map[string]bool) plan {
	p := plan{}
	primary := ""
	for _, mon := range ms {
		p[mon.ID] = &monPlan{Use: !unused[mon.ID], Mode: layout.ModeMaximized}
		if primary == "" {
			primary = mon.ID // sorted: primary first
		}
	}
	seen := map[string]bool{}
	for _, w := range l.Windows {
		mp := p[w.Monitor]
		if mp == nil && w.Monitor != "" && w.Panel != "" {
			continue // waits for its monitor
		}
		if mp == nil {
			mp = p[primary]
		}
		if mp == nil {
			continue
		}
		if len(mp.Panels) == 0 && !mp.Main {
			mp.Mode = w.Mode
		}
		if w.Panel == "" {
			mp.Main = true
		} else if !seen[w.Panel] {
			seen[w.Panel] = true
			mp.Panels = append(mp.Panels, w.Panel)
		}
	}
	return p
}

// keepWaiting adds the windows of base that wait for a monitor not in ms to l, unless
// the plan put that panel somewhere else: plugging the monitor back in brings them back.
func keepWaiting(l *layout.Layout, base layout.Layout, ms []display.Monitor) {
	present := map[string]bool{}
	for _, mon := range ms {
		present[mon.ID] = true
	}
	placed := map[string]bool{}
	for _, w := range l.Windows {
		placed[w.Panel] = true
	}
	for _, w := range base.Windows {
		if w.Panel != "" && w.Monitor != "" && !present[w.Monitor] && !placed[w.Panel] {
			l.Windows = append(l.Windows, w)
			placed[w.Panel] = true
		}
	}
}

// setMain puts the main window on monitor id only.
func (p plan) setMain(id string) {
	for k, mp := range p {
		mp.Main = k == id
	}
}

// setPanel adds or removes a panel on monitor id; a panel lives on one monitor only.
func (p plan) setPanel(id, panel string, on bool) {
	for k, mp := range p {
		var kept []string
		for _, x := range mp.Panels {
			if x != panel {
				kept = append(kept, x)
			}
		}
		if on && k == id {
			kept = append(kept, panel)
		}
		mp.Panels = kept
	}
}

// presets offered for the connected monitors.
type preset struct {
	Label string
	Build func() plan
	Page  string // page to show, "" keeps the current one
}

func (m *Manager) presets(ms []display.Monitor) []preset {
	if len(ms) == 0 {
		return nil
	}
	wayland := !m.disp.CanPosition()
	empty := func() plan {
		p := plan{}
		for _, mon := range ms {
			p[mon.ID] = &monPlan{Use: true, Mode: layout.ModeMaximized}
		}
		return p
	}
	primary := ms[0].ID
	out := []preset{{Label: "Ein Monitor", Build: func() plan {
		p := empty()
		p[primary].Main = true
		return p
	}}}
	if len(ms) >= 2 {
		other := ms[1].ID // primary first, then left to right
		out = append(out, preset{Label: "Zwei Monitore: Schnitt + Programm-Vollbild", Page: page.Edit, Build: func() plan {
			p := empty()
			p[primary].Main = true
			p[other].Mode = layout.ModeFullscreen
			p[other].Panels = []string{page.PanelProgram}
			return p
		}})
	}
	if len(ms) >= 3 {
		left, mid, right := threeAcross(ms)
		label := "Drei Monitore: Medien | Schnitt | Programm + Scopes"
		if wayland {
			// Only fullscreen reaches a chosen monitor under Wayland, so each side
			// monitor gets one fullscreen panel; a second window there could not be placed.
			label = "Drei Monitore: Medien | Schnitt | Programm"
		}
		out = append(out, preset{Label: label, Page: page.Edit, Build: func() plan {
			p := empty()
			p[mid].Main = true
			p[left].Panels = []string{page.PanelMediaPool}
			p[right].Panels = []string{page.PanelProgram, page.PanelScopes}
			p[right].Mode = layout.ModeWindow // side by side
			if wayland {
				p[left].Mode = layout.ModeFullscreen
				p[right].Mode = layout.ModeFullscreen
				p[right].Panels = []string{page.PanelProgram}
			}
			return p
		}})
	}
	return out
}

// threeAcross picks left, middle and right monitors: the primary in the middle when it
// has neighbours on both sides, otherwise the first three from the left.
func threeAcross(ms []display.Monitor) (left, mid, right string) {
	byX := append([]display.Monitor(nil), ms...)
	sort.SliceStable(byX, func(i, j int) bool { return byX[i].X < byX[j].X })
	pi := 0
	for i, mon := range byX {
		if mon.Primary {
			pi = i
		}
	}
	if pi == 0 || pi == len(byX)-1 {
		pi = 1
	}
	return byX[pi-1].ID, byX[pi].ID, byX[pi+1].ID
}

// tileWeight gives picture panels and the main window more room when windows share a
// monitor side by side.
func tileWeight(panel string) int {
	switch panel {
	case "":
		return 3
	case page.PanelProgram, page.PanelCleanFeed, page.PanelTimeline:
		return 2
	}
	return 1
}

// planLayout turns a plan into a layout (pages, name and active page from base).
// Several windows on one monitor in window mode are tiled side by side; in fullscreen
// mode the first one is fullscreen and the others open as tiled windows.
func (m *Manager) planLayout(p plan, ms []display.Monitor, base layout.Layout) layout.Layout {
	l := base
	l.Windows = nil
	pages := map[string]layout.Page{}
	for k, v := range base.Pages {
		if k != workspaceKey {
			pages[k] = v
		}
	}
	var unused []string
	mainMon := ""
	for _, mon := range ms {
		if mp := p[mon.ID]; mp != nil && mp.Use && mp.Main {
			mainMon = mon.ID
		}
	}
	if mainMon == "" { // the main window must live somewhere
		for _, mon := range ms {
			if mp := p[mon.ID]; mp == nil || mp.Use {
				mainMon = mon.ID
				break
			}
		}
	}
	for _, mon := range ms {
		mp := p[mon.ID]
		if mp == nil {
			continue
		}
		if !mp.Use {
			unused = append(unused, mon.ID)
			continue
		}
		var ids []string // "" = main window
		if mon.ID == mainMon {
			ids = append(ids, "")
		}
		for _, panel := range mp.Panels {
			if _, ok := page.PanelByID(panel); ok {
				ids = append(ids, panel)
			}
		}
		if len(ids) == 0 {
			continue
		}
		tiled := ids
		first := ""
		if mp.Mode != layout.ModeWindow && len(ids) > 0 {
			first = ids[0]
			tiled = ids[1:]
		}
		rects := m.tiles(mon, tiled)
		for i, id := range ids {
			w := layout.Window{ID: id, Panel: id, Monitor: mon.ID, Mode: mp.Mode}
			if id == "" {
				w.ID = "main"
			}
			if mp.Mode != layout.ModeWindow && i == 0 && first == id {
				l.Windows = append(l.Windows, w)
				continue
			}
			w.Mode = layout.ModeWindow
			w.Rect = rects[id]
			l.Windows = append(l.Windows, w)
		}
	}
	if mainMon == "" || !hasMain(l.Windows) {
		l.Windows = append([]layout.Window{{ID: "main", Monitor: mainMon, Mode: layout.ModeMaximized}}, l.Windows...)
	}
	if len(unused) > 0 {
		pages[workspaceKey] = layout.Page{Extra: map[string]string{"unused": joinIDs(unused)}}
	}
	l.Pages = pages
	return l
}

func hasMain(ws []layout.Window) bool {
	for _, w := range ws {
		if w.Panel == "" {
			return true
		}
	}
	return false
}

func joinIDs(ids []string) string {
	sort.Strings(ids)
	s := ""
	for i, id := range ids {
		if i > 0 {
			s += ","
		}
		s += id
	}
	return s
}

// tiles splits a monitor into weighted columns for windowed placement (keeping a margin
// for window decorations). Under Wayland, where positions are ignored, every window
// gets a comfortable default size instead.
func (m *Manager) tiles(mon display.Monitor, ids []string) map[string]layout.Rect {
	out := map[string]layout.Rect{}
	if len(ids) == 0 {
		return out
	}
	if !m.disp.CanPosition() {
		for i, id := range ids {
			out[id] = m.defaultRect(mon.ID, i)
		}
		return out
	}
	const margin, top = 8, 40
	total := 0
	for _, id := range ids {
		total += tileWeight(id)
	}
	x := margin
	avail := mon.W - margin*(len(ids)+1)
	for i, id := range ids {
		w := avail * tileWeight(id) / total
		if i == len(ids)-1 {
			w = mon.W - margin - x
		}
		out[id] = layout.Rect{X: x, Y: top, W: w, H: mon.H - top - margin}
		x += w + margin
	}
	return out
}
