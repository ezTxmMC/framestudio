package workspace

import (
	"image/png"
	"os"
	"reflect"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
)

// tapMonitor taps the centre of a monitor in the arrangement drawing.
func tapMonitor(t *testing.T, md *monitorDialog, id string) {
	t.Helper()
	p, ok := md.arr.boxes[id]
	if !ok {
		t.Fatalf("monitor %s not drawn (%v)", id, md.arr.boxes)
	}
	s := md.arr.sizes[id]
	md.arr.Tapped(&fyne.PointEvent{Position: p.Add(fyne.NewPos(s.Width/2, s.Height/2))})
}

func openDialog(t *testing.T, h *harness) *monitorDialog {
	t.Helper()
	h.host.reg.Run("workspace.monitors")
	md := h.m.dlg
	if md == nil {
		t.Fatal("dialog not open")
	}
	return md
}

func TestMonitorDialogAssignsAndApplies(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	md := openDialog(t, h)
	if md.selected != "DP-2" || !md.plan["DP-2"].Main {
		t.Fatalf("initial selection %s %+v", md.selected, md.plan["DP-2"])
	}
	tapMonitor(t, md, "HDMI-A-1")
	if md.selected != "HDMI-A-1" || !strings.HasPrefix(md.heading.Text, "HDMI-A-1") {
		t.Fatalf("selected %s heading %q", md.selected, md.heading.Text)
	}
	md.content.SetSelected([]string{"Programm-Monitor", "Scopes"})
	md.modes.buttons[0].OnTapped() // Fenster
	if got := md.plan["HDMI-A-1"]; got.Mode != layout.ModeWindow || !reflect.DeepEqual(got.Panels, []string{page.PanelProgram, page.PanelScopes}) {
		t.Fatalf("plan %+v", got)
	}
	// Moving the program to DP-3 takes it off HDMI-A-1.
	tapMonitor(t, md, "DP-3")
	md.content.SetSelected([]string{"Programm-Monitor"})
	md.modes.buttons[2].OnTapped()
	if !reflect.DeepEqual(md.plan["HDMI-A-1"].Panels, []string{page.PanelScopes}) {
		t.Fatalf("program not moved: %+v", md.plan["HDMI-A-1"])
	}
	if len(h.m.Windows()) != 0 {
		t.Fatal("applied before Anwenden")
	}
	md.apply.OnTapped()
	if got := h.m.Windows(); !reflect.DeepEqual(got, []string{page.PanelProgram, page.PanelScopes}) {
		t.Fatalf("windows %v", got)
	}
	if s := h.state(page.PanelProgram); s.mon != "DP-3" || s.mode != layout.ModeFullscreen {
		t.Fatalf("program %+v", s)
	}
	if s := h.state(page.PanelScopes); s.mon != "HDMI-A-1" || s.mode != layout.ModeWindow || s.r.W < 2000 {
		t.Fatalf("scopes %+v", s)
	}
	// The main window can move: tick it on DP-3, it leaves DP-2.
	md.content.SetSelected([]string{"Hauptfenster", "Programm-Monitor"})
	if md.plan["DP-2"].Main || !md.plan["DP-3"].Main {
		t.Fatal("main window must live on one monitor")
	}
	// "Verwenden" off clears the monitor.
	tapMonitor(t, md, "HDMI-A-1")
	md.use.SetChecked(false)
	if md.plan["HDMI-A-1"].Use || len(md.plan["HDMI-A-1"].Panels) != 0 || !md.content.Disabled() {
		t.Fatalf("unused %+v", md.plan["HDMI-A-1"])
	}
	md.apply.OnTapped()
	if !h.m.unused["HDMI-A-1"] || h.m.Window(page.PanelScopes) != nil {
		t.Fatalf("unused not applied: %v %v", h.m.unused, h.m.Windows())
	}
	if s := h.disp.state(h.host.main); s.mon != "DP-3" {
		t.Fatalf("main %+v", s)
	}
	md.hide()
	if h.m.dlg != nil {
		t.Fatal("dialog still registered")
	}
}

func TestMonitorDialogPresetAndHint(t *testing.T) {
	h := newHarness(t, false)
	md := openDialog(t, h)
	markup := test.RenderObjectToMarkup(md.presetBox)
	for _, want := range []string{"Ein Monitor", "Zwei Monitore", "Drei Monitore"} {
		if !strings.Contains(markup, want) {
			t.Fatalf("preset %q missing", want)
		}
	}
	md.presetBox.Objects[2].(interface{ Tapped(*fyne.PointEvent) }).Tapped(nil)
	if md.page != page.Edit || !md.plan["DP-2"].Main || !reflect.DeepEqual(md.plan["DP-3"].Panels, []string{page.PanelMediaPool}) {
		t.Fatalf("preset plan %+v", md.plan)
	}
	md.apply.OnTapped()
	if got := h.m.Windows(); !reflect.DeepEqual(got, []string{page.PanelMediaPool, page.PanelProgram, page.PanelScopes}) {
		t.Fatalf("windows %v", got)
	}
	hint := false
	for _, o := range test.LaidOutObjects(h.host.main.Canvas().Overlays().Top()) {
		if l, ok := o.(*widget.Label); ok && l.Text == WaylandHint && l.Visible() {
			hint = true
		}
	}
	if !hint {
		t.Fatal("Wayland hint missing")
	}
	// Hot-plug while open (no pending edits) reloads the monitors.
	h.disp.setMonitors(userMonitors()[:2])
	if len(md.ms) != 2 || len(md.presetBox.Objects) != 2 {
		t.Fatalf("not refreshed: %d monitors, %d presets", len(md.ms), len(md.presetBox.Objects))
	}
}

func TestMonitorDialogWithoutMonitors(t *testing.T) {
	h := newHarness(t, true)
	h.disp.ms = nil
	md := openDialog(t, h)
	if md.selected != "" || !md.apply.Disabled() {
		t.Fatal("empty dialog")
	}
	if !strings.Contains(test.RenderToMarkup(h.host.main.Canvas()), "Keine Monitore erkannt") {
		t.Fatal("no empty text")
	}
}

func writePNG(t *testing.T, path string, c fyne.Canvas) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, c.Capture()); err != nil {
		t.Fatal(err)
	}
}

// TestScreenshot renders the monitor dialog with the user's three monitors and a detached
// window header (FS_SCREENSHOT=1).
func TestScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT=1 to write /tmp/fs-r3-monitors.png and /tmp/fs-r3-detached.png")
	}
	h := newHarness(t, false)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelProgram, "HDMI-A-1", true)
	h.m.Detach(page.PanelScopes, "HDMI-A-1", false)
	h.m.open(page.PanelMediaPool, "DP-3", layout.ModeMaximized, layout.Rect{}, "")
	md := openDialog(t, h)
	tapMonitor(t, md, "HDMI-A-1")
	h.host.main.Resize(fyne.NewSize(1100, 720))
	writePNG(t, "/tmp/fs-r3-monitors.png", h.host.main.Canvas())

	d := h.m.find(page.PanelScopes)
	d.win.Resize(fyne.NewSize(960, 300))
	writePNG(t, "/tmp/fs-r3-detached.png", d.win.Canvas())
	p := h.m.find(page.PanelProgram)
	p.setFullscreen(false)
	p.win.Resize(fyne.NewSize(960, 300))
	writePNG(t, "/tmp/fs-r3-detached-program.png", p.win.Canvas())
}
