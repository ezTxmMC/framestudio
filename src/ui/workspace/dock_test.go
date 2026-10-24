package workspace

import (
	"bytes"
	"encoding/json"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	"fyne.io/fyne/v2"
	"testing"
)

type dockingHost struct {
	*fakeHost
	dock   fyne.CanvasObject
	offset float64
}

func (h *dockingHost) SetMainDock(o fyne.CanvasObject) { h.dock = o }
func (h *dockingHost) MainDockOffset() float64 {
	if h.offset == 0 {
		return 0.7
	}
	return h.offset
}
func (h *dockingHost) SetMainDockOffset(v float64) { h.offset = v }
func dockHarness(t *testing.T) (*harness, *dockingHost) {
	h := newHarness(t, true)
	h.m.shutdown(false)
	host := &dockingHost{fakeHost: h.host}
	h.m = NewWithDisplay(host, h.disp)
	return h, host
}
func TestPanelsShareMonitorAndPrimaryWindow(t *testing.T) {
	h, host := dockHarness(t)
	before := len(host.app.Driver().AllWindows())
	h.m.Detach(page.PanelTimeline, "DP-2", false)
	if h.m.Window(page.PanelTimeline) != host.main || host.dock == nil {
		t.Fatal("primary dock created a separate window")
	}
	if len(host.app.Driver().AllWindows()) != before {
		t.Fatal("primary docking opened a window")
	}
	h.m.Detach(page.PanelProgram, "DP-3", false)
	h.m.Detach(page.PanelScopes, "DP-3", false)
	if h.m.Window(page.PanelProgram) != h.m.Window(page.PanelScopes) {
		t.Fatal("same monitor has multiple windows")
	}
	d := h.m.find(page.PanelProgram)
	d.dockMode = "horizontal"
	d.refreshDock()
	d.dockSplits[""].SetOffset(0.63)
	l := h.m.Current()
	if len(l.Windows) != 4 {
		t.Fatalf("panels lost from capture: %+v", l.Windows)
	}
	h.m.CloseDetached()
	if host.dock != nil {
		t.Fatal("primary dock not removed")
	}
	h.m.Apply(l)
	d = h.m.find(page.PanelScopes)
	if d == nil || d.dockMode != "horizontal" || d.dockSplits[""].Offset != 0.63 {
		t.Fatal("dock split not restored")
	}
	win := d.win
	h.m.ToggleDetached(page.PanelProgram)
	if h.m.Window(page.PanelScopes) != win || h.m.Window(page.PanelProgram) != nil {
		t.Fatal("closing first panel closed its dock peers")
	}
}
func TestWorkspaceLayoutsAndMonitorReconnection(t *testing.T) {
	h, _ := dockHarness(t)
	h.m.Detach(page.PanelProgram, "DP-3", false)
	h.m.Detach(page.PanelScopes, "DP-3", false)
	h.m.SwitchWorkspace(page.Audio)
	if len(h.m.Windows()) != 0 {
		t.Fatal("edit docks carried into audio workspace")
	}
	h.m.Detach(page.PanelMixer, "HDMI-A-1", false)
	h.m.SwitchWorkspace(page.Edit)
	if len(h.m.Windows()) != 2 || h.m.Window(page.PanelProgram) == nil || h.m.Window(page.PanelMixer) != nil {
		t.Fatal("edit workspace not restored")
	}
	l := h.m.Current()
	if err := layout.Save(l, false); err != nil {
		t.Fatal(err)
	}
	saved, err := layout.Load(l.Name, false)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(l.Workspaces)
	after, _ := json.Marshal(saved.Workspaces)
	if !bytes.Equal(before, after) {
		t.Fatal("workspace layouts not persisted")
	}
	h.disp.setMonitors(userMonitors()[:1])
	if h.m.Window(page.PanelProgram) != h.host.main {
		t.Fatal("missing monitor did not dock in main window")
	}
	if h.m.Window(page.PanelScopes) != h.host.main {
		t.Fatal("fallback opened multiple windows")
	}
	h.disp.setMonitors(userMonitors())
	if h.m.Window(page.PanelProgram) == h.host.main || h.m.Window(page.PanelProgram) != h.m.Window(page.PanelScopes) {
		t.Fatal("panels did not return to one window on their monitor")
	}
	h.m.SwitchWorkspace(page.Audio)
	if h.m.Window(page.PanelMixer) == nil || len(h.m.Windows()) != 1 {
		t.Fatal("audio workspace not retained")
	}
}

func TestMovingMainOntoDockMonitorMergesWindows(t *testing.T) {
	h, _ := dockHarness(t)
	h.m.Detach(page.PanelTimeline, "DP-2", false)
	h.m.Detach(page.PanelProgram, "DP-3", false)
	h.m.Detach(page.PanelScopes, "DP-3", false)
	h.m.moveMain("DP-3")
	for _, id := range []string{page.PanelTimeline, page.PanelProgram, page.PanelScopes} {
		if h.m.Window(id) != h.host.main {
			t.Fatalf("%s remains in a second window on the main monitor", id)
		}
	}
	if len(h.m.wins) != 1 {
		t.Fatalf("%d dock hosts on main monitor", len(h.m.wins))
	}
}
