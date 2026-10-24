package workspace

import (
	"framestudio-proto/src/ui/page"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// MainDockHost lets panels share the primary window as well as auxiliary monitors.
// Hosts without a primary docking surface retain their standalone-window contract.
type MainDockHost interface{ SetMainDock(fyne.CanvasObject) }

func (m *Manager) grouping() bool { _, ok := m.host.(MainDockHost); return ok }

type dockMember struct {
	info  page.PanelInfo
	panel page.Panel
	ctx   *page.Context
	body  *fyne.Container
	built bool
}

func (d *detached) panelIDs() []string {
	ids := []string{d.id}
	for _, member := range d.members {
		ids = append(ids, member.info.ID)
	}
	return ids
}
func (d *detached) hasPanel(id string) bool {
	for _, candidate := range d.panelIDs() {
		if candidate == id {
			return true
		}
	}
	return false
}
func (d *detached) activePanel() string {
	if d.hasPanel(d.activeID) {
		return d.activeID
	}
	return d.id
}
func (d *detached) activeContent() page.Panel {
	for _, member := range d.members {
		if member.info.ID == d.activePanel() {
			return member.panel
		}
	}
	return d.panel
}
func (d *detached) addPanel(info page.PanelInfo) {
	if d.hasPanel(info.ID) {
		d.activeID = info.ID
		d.refreshDock()
		return
	}
	d.members = append(d.members, &dockMember{info: info, body: container.NewStack()})
	d.activeID = info.ID
	d.rebuildMembers()
	d.refreshDock()
}
func (d *detached) rebuildMembers() {
	ctx := d.m.host.ActiveContext()
	for _, member := range d.members {
		if member.built && member.ctx == ctx {
			continue
		}
		if member.panel != nil {
			member.panel.Close()
			member.panel = nil
		}
		member.ctx, member.built = ctx, true
		var content fyne.CanvasObject
		if ctx != nil && member.info.New != nil {
			c := *ctx
			c.Window = d.win
			member.panel = member.info.New(&c)
			content = member.panel.Content()
		} else {
			content = d.emptyState("Kein Projekt geöffnet", member.info.Title)
		}
		member.body.Objects = []fyne.CanvasObject{content}
		member.body.Refresh()
	}
}
func (d *detached) captureDock() map[string]float64 {
	out := map[string]float64{}
	for path, offset := range d.dockOffsets {
		out[path] = offset
	}
	if d.inMain {
		if h, ok := d.m.host.(MainDockSizer); ok {
			out["$main"] = h.MainDockOffset()
		}
	}
	for path, split := range d.dockSplits {
		out[path] = split.Offset
	}
	return out
}
func (d *detached) refreshDock() {
	if d.dockArea == nil {
		return
	}
	if d.idle != nil {
		if d.cleanFeed() {
			d.idle.Show()
		} else {
			d.idle.stop()
			d.idle.Hide()
		}
	}
	d.dockOffsets = d.captureDock()
	d.dockSplits = map[string]*container.Split{}
	ids := d.panelIDs()
	if d.inMain {
		if host, ok := d.m.host.(interface{ SetMainDockVertical(bool) }); ok {
			host.SetMainDockVertical(d.hasPanel(page.PanelTimeline))
		}
	}
	body := func(id string) fyne.CanvasObject {
		if id == d.id {
			return d.body
		}
		for _, member := range d.members {
			if member.info.ID == id {
				return member.body
			}
		}
		return container.NewStack()
	}
	title := func(id string) string {
		if id == d.id {
			return d.info.Title
		}
		for _, member := range d.members {
			if member.info.ID == id {
				return member.info.Title
			}
		}
		return id
	}
	var area fyne.CanvasObject
	if d.dockMode == "horizontal" || d.dockMode == "vertical" {
		var split func([]string, string) fyne.CanvasObject
		split = func(list []string, path string) fyne.CanvasObject {
			id := list[0]
			pane := container.NewBorder(container.NewBorder(nil, nil, widget.NewButton(title(id), func() { d.activeID = id }), widget.NewButton("×", func() { d.removePanel(id) })), nil, nil, nil, body(id))
			if len(list) == 1 {
				return pane
			}
			var result *container.Split
			if d.dockMode == "vertical" {
				result = container.NewVSplit(pane, split(list[1:], path+"r"))
			} else {
				result = container.NewHSplit(pane, split(list[1:], path+"r"))
			}
			offset := 1 / float64(len(list))
			if saved := d.dockOffsets[path]; saved > 0 && saved < 1 {
				offset = saved
			}
			result.SetOffset(offset)
			d.dockSplits[path] = result
			return result
		}
		area = split(ids, "")
	} else if len(ids) == 1 {
		area = body(ids[0])
	} else {
		tabs := container.NewAppTabs()
		for _, id := range ids {
			tabs.Append(container.NewTabItem(title(id), body(id)))
		}
		tabs.OnSelected = func(item *container.TabItem) {
			for i, candidate := range tabs.Items {
				if item == candidate {
					d.activeID = ids[i]
					break
				}
			}
		}
		for i, id := range ids {
			if id == d.activePanel() {
				tabs.SelectIndex(i)
			}
		}
		area = tabs
	}
	if d.m.grouping() && !(d.cleanFeed() && d.win.FullScreen()) {
		mode := widget.NewSelect([]string{"Tabs", "Nebeneinander", "Untereinander"}, nil)
		index := 0
		if d.dockMode == "horizontal" {
			index = 1
		}
		if d.dockMode == "vertical" {
			index = 2
		}
		mode.SetSelectedIndex(index)
		mode.OnChanged = func(string) {
			d.dockMode = []string{"tabs", "horizontal", "vertical"}[mode.SelectedIndex()]
			d.refreshDock()
		}
		closePanel := widget.NewButton("Panel schließen", func() { d.removePanel(d.activePanel()) })
		heading := container.NewBorder(nil, nil, mode, closePanel)
		if len(ids) == 1 {
			heading.Add(widget.NewLabel(title(ids[0])))
		}
		area = container.NewBorder(heading, nil, nil, nil, area)
	}
	d.dockArea.Objects = []fyne.CanvasObject{area}
	d.dockArea.Refresh()
}
func (d *detached) removePanel(id string) {
	if id == d.id {
		if len(d.members) == 0 {
			d.close()
			return
		}
		if d.panel != nil {
			d.panel.Close()
		}
		next := d.members[0]
		d.members = d.members[1:]
		d.id, d.info, d.panel, d.ctx, d.body, d.built = next.info.ID, next.info, next.panel, next.ctx, next.body, next.built
		d.title.Text = d.info.Title
		d.title.Refresh()
	} else {
		for i, member := range d.members {
			if member.info.ID == id {
				if member.panel != nil {
					member.panel.Close()
				}
				d.members = append(d.members[:i], d.members[i+1:]...)
				break
			}
		}
	}
	d.activeID = d.id
	d.refreshDock()
	d.arranged = ""
	d.arrange()
	d.m.refresh()
}

// MainDockSizer saves the divider between the page and docked primary panels.
type MainDockSizer interface {
	MainDockOffset() float64
	SetMainDockOffset(float64)
}

// DispatchMainDock routes editing keys to the selected primary dock panel.
func (m *Manager) DispatchMainDock(key fyne.KeyName, mods fyne.KeyModifier) bool {
	for _, d := range m.wins {
		if d.inMain && !d.closing && !d.modal() {
			panel := d.activeContent()
			if shortcut, ok := panel.(page.ShortcutPanel); ok && shortcut.Shortcut(key, mods) {
				return true
			}
			if mods&(fyne.KeyModifierControl|fyne.KeyModifierAlt|fyne.KeyModifierSuper) == 0 && panel != nil && panel.TypedKey(&fyne.KeyEvent{Name: key}) {
				return true
			}
		}
	}
	return false
}
