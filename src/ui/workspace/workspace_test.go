package workspace

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
)

func ids(ws []layout.Window) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.ID+"@"+w.Monitor+":"+w.Mode)
	}
	return out
}

func presetByPrefix(t *testing.T, m *Manager, prefix string) preset {
	t.Helper()
	for _, p := range m.presets(m.monitors()) {
		if strings.HasPrefix(p.Label, prefix) {
			return p
		}
	}
	t.Fatalf("no preset %q", prefix)
	return preset{}
}

func TestPresetsForOneTwoThreeMonitors(t *testing.T) {
	h := newHarness(t, true)
	all := userMonitors()
	for n, want := range map[int][]string{
		1: {"Ein Monitor"},
		2: {"Ein Monitor", "Zwei Monitore: Schnitt + Programm-Vollbild"},
		3: {"Ein Monitor", "Zwei Monitore: Schnitt + Programm-Vollbild", "Drei Monitore: Medien | Schnitt | Programm + Scopes"},
	} {
		h.disp.ms = all[:n]
		var got []string
		for _, p := range h.m.presets(h.m.monitors()) {
			got = append(got, p.Label)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%d monitors: presets %v", n, got)
		}
	}
	h.disp.ms = all
	ms := h.m.monitors()
	base := h.m.Current()

	one := h.m.planLayout(presetByPrefix(t, h.m, "Ein").Build(), ms, base)
	if got := ids(one.Windows); !reflect.DeepEqual(got, []string{"main@DP-2:maximized"}) {
		t.Fatalf("one monitor: %v", got)
	}
	two := h.m.planLayout(presetByPrefix(t, h.m, "Zwei").Build(), ms, base)
	if got := ids(two.Windows); !reflect.DeepEqual(got, []string{"main@DP-2:maximized", "program@DP-3:fullscreen"}) {
		t.Fatalf("two monitors: %v", got)
	}
	three := h.m.planLayout(presetByPrefix(t, h.m, "Drei").Build(), ms, base)
	want := []string{"main@DP-2:maximized", "mediapool@DP-3:maximized", "program@HDMI-A-1:window", "scopes@HDMI-A-1:window"}
	if got := ids(three.Windows); !reflect.DeepEqual(got, want) {
		t.Fatalf("three monitors: %v", got)
	}
	// Program and scopes share the right monitor side by side, program wider.
	pr, sc := three.Windows[2].Rect, three.Windows[3].Rect
	if pr.W <= sc.W || pr.X+pr.W > sc.X || sc.X+sc.W > 2560 || pr.H < 1300 {
		t.Fatalf("tiles: program %+v scopes %+v", pr, sc)
	}
	if three.Pages[page.Edit].Splits["viewer"] != 0.42 {
		t.Fatal("page layouts lost")
	}
}

func TestThreeMonitorPresetOnWaylandUsesFullscreen(t *testing.T) {
	h := newHarness(t, false)
	l := h.m.planLayout(presetByPrefix(t, h.m, "Drei").Build(), h.m.monitors(), h.m.Current())
	// A second window on HDMI-A-1 could not be placed there under Wayland: no scopes.
	want := []string{"main@DP-2:maximized", "mediapool@DP-3:fullscreen", "program@HDMI-A-1:fullscreen"}
	if got := ids(l.Windows); !reflect.DeepEqual(got, want) {
		t.Fatalf("wayland three monitors: %v", got)
	}
}

func TestThreeAcrossPutsPrimaryInTheMiddle(t *testing.T) {
	ms := userMonitors()
	l, m, r := threeAcross(ms)
	if l != "DP-3" || m != "DP-2" || r != "HDMI-A-1" {
		t.Fatalf("got %s %s %s", l, m, r)
	}
	ms[0].Primary, ms[1].Primary = false, true // primary at the left edge
	l, m, r = threeAcross(ms)
	if l != "DP-3" || m != "DP-2" || r != "HDMI-A-1" {
		t.Fatalf("edge primary: %s %s %s", l, m, r)
	}
}

func TestApplyPresetOpensWindowsOnTheirMonitors(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	p := presetByPrefix(t, h.m, "Drei")
	base := h.m.Current()
	base.ActivePage = p.Page
	h.m.Apply(h.m.planLayout(p.Build(), h.m.monitors(), base))
	if got := h.m.Windows(); !reflect.DeepEqual(got, []string{page.PanelMediaPool, page.PanelProgram, page.PanelScopes}) {
		t.Fatalf("windows %v", got)
	}
	if s := h.state(page.PanelMediaPool); s.mon != "DP-3" || s.mode != layout.ModeMaximized {
		t.Fatalf("mediapool %+v", s)
	}
	if s := h.state(page.PanelProgram); s.mon != "HDMI-A-1" || s.mode != layout.ModeWindow || s.r.W < 1000 {
		t.Fatalf("program %+v", s)
	}
	if s := h.disp.state(h.host.main); s.mon != "DP-2" || s.mode != layout.ModeMaximized {
		t.Fatalf("main %+v", s)
	}
	if h.host.active != page.Edit {
		t.Fatalf("active page %q", h.host.active)
	}
	if w := h.m.Window(page.PanelProgram); w.Title() != "Programm-Monitor — Frame Studio" {
		t.Fatalf("title %q", w.Title())
	}
	// Back to one monitor closes the detached windows.
	h.m.Apply(h.m.planLayout(presetByPrefix(t, h.m, "Ein").Build(), h.m.monitors(), h.m.Current()))
	if len(h.m.Windows()) != 0 {
		t.Fatalf("still open: %v", h.m.Windows())
	}
	for _, p := range built {
		if !p.closed {
			t.Fatalf("panel %s not closed", p.id)
		}
	}
}

func TestMissingMonitorFallsBackWindowedAndReturns(t *testing.T) {
	h := newHarness(t, true)
	l := layout.Default()
	l.Main().Monitor = "DP-2"
	l.Windows = append(l.Windows,
		layout.Window{ID: "program", Panel: page.PanelProgram, Monitor: "DP-9", Mode: layout.ModeFullscreen},
		layout.Window{ID: "scopes", Panel: page.PanelScopes, Monitor: "HDMI-A-1", Mode: layout.ModeWindow, Rect: layout.Rect{X: 10, Y: 20, W: 900, H: 600}})
	h.m.Apply(l)
	if len(h.m.Windows()) != 2 {
		t.Fatalf("a panel got lost: %v", h.m.Windows())
	}
	s := h.state(page.PanelProgram)
	if s.mon != "DP-2" || s.mode != layout.ModeWindow || h.m.Window(page.PanelProgram).FullScreen() {
		t.Fatalf("fallback %+v", s)
	}
	if s := h.state(page.PanelScopes); s.mon != "HDMI-A-1" || s.r != (layout.Rect{X: 10, Y: 20, W: 900, H: 600}) {
		t.Fatalf("scopes %+v", s)
	}
	if !strings.Contains(h.m.find(page.PanelProgram).where.Text, "Ersatz für DP-9") {
		t.Fatalf("header %q", h.m.find(page.PanelProgram).where.Text)
	}
	// The saved layout keeps the wish while the monitor is away.
	cur := h.m.Current()
	if got := ids(cur.Windows)[1]; got != "program@DP-9:fullscreen" {
		t.Fatalf("captured %s", got)
	}
	// The monitor comes back: fullscreen there again.
	h.disp.setMonitors(append(userMonitors(), display.Monitor{ID: "DP-9", Name: "DP-9 · 1920×1080", X: 7680, W: 1920, H: 1080}))
	if s := h.state(page.PanelProgram); s.mon != "DP-9" || s.mode != layout.ModeFullscreen {
		t.Fatalf("after replug %+v", s)
	}
	// And leaves again: windowed on the main monitor.
	h.disp.setMonitors(userMonitors())
	if s := h.state(page.PanelProgram); s.mon != "DP-2" || s.mode != layout.ModeWindow {
		t.Fatalf("after unplug %+v", s)
	}
	if len(h.errs) != 0 {
		t.Fatalf("errors: %v", h.errs)
	}
}

func TestMainWindowOnMissingMonitor(t *testing.T) {
	h := newHarness(t, true)
	l := layout.Default()
	l.Main().Monitor = "DP-7"
	l.Main().Mode = layout.ModeFullscreen
	h.m.Apply(l)
	if s := h.disp.state(h.host.main); s.mon != "DP-2" || s.mode != layout.ModeMaximized {
		t.Fatalf("main %+v", s)
	}
}

func TestCaptureRestoreRoundTrip(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelProgram, "HDMI-A-1", true)
	h.m.Detach(page.PanelTimeline, "DP-3", false)
	h.m.setUnused("DP-3", false)
	h.host.active = page.Color
	h.m.name = "Drei Monitore"
	before := h.m.Current()
	if err := layout.Save(before, false); err != nil {
		t.Fatal(err)
	}
	h.m.CloseDetached()
	h.host.pages = map[string]layout.Page{}
	h.host.active = page.Edit
	if len(h.m.Windows()) != 0 {
		t.Fatal("close detached")
	}
	h.m.LoadLayout("Drei Monitore")
	after := h.m.Current()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("round trip differs\nbefore %+v\nafter  %+v", before, after)
	}
	if h.host.active != page.Color || h.host.applied[page.Edit].Splits["viewer"] != 0.42 {
		t.Fatalf("pages not restored: %q %+v", h.host.active, h.host.applied)
	}
}

func TestUnusedMonitorPersistsAndIsAvoided(t *testing.T) {
	h := newHarness(t, true)
	h.m.setUnused("DP-3", true)
	if got := h.m.bestOther(); got != "HDMI-A-1" {
		t.Fatalf("best other = %s", got)
	}
	l := h.m.Current()
	if l.Pages[workspaceKey].Extra["unused"] != "DP-3" {
		t.Fatalf("not captured: %+v", l.Pages)
	}
	h.m.setUnused("DP-3", false)
	l.Windows = append(l.Windows, layout.Window{ID: "program", Panel: page.PanelProgram, Monitor: "DP-3", Mode: layout.ModeFullscreen})
	h.m.Apply(l)
	if !h.m.unused["DP-3"] {
		t.Fatal("unused not restored")
	}
	if s := h.state(page.PanelProgram); s.mon != "DP-2" || s.mode != layout.ModeWindow {
		t.Fatalf("window on unused monitor: %+v", s)
	}
	if _, ok := h.host.applied[workspaceKey]; ok {
		t.Fatal("workspace key leaked to pages")
	}
}

func TestRebuildOnActiveChange(t *testing.T) {
	h := newHarness(t, true)
	ctxA := &page.Context{ShowErr: func(error) {}}
	h.host.ctx = ctxA
	h.m.Detach(page.PanelProgram, "DP-3", false)
	first := lastPanel(page.PanelProgram)
	if first == nil || first.ctx.Window != h.m.Window(page.PanelProgram) || first.ctx.ShowErr == nil {
		t.Fatal("panel not built with a context for its window")
	}
	h.host.setActive(ctxA) // same tab: no rebuild
	if lastPanel(page.PanelProgram) != first {
		t.Fatal("rebuilt for the same context")
	}
	ctxB := &page.Context{}
	h.host.setActive(ctxB)
	second := lastPanel(page.PanelProgram)
	if !first.closed || second == first || second.closed {
		t.Fatal("panel not rebuilt for the new tab")
	}
	h.host.setActive(nil)
	if !second.closed || h.m.find(page.PanelProgram).panel != nil {
		t.Fatal("panel kept without a project")
	}
	if !strings.Contains(test.RenderObjectToMarkup(h.m.find(page.PanelProgram).body), "Kein Projekt geöffnet") {
		t.Fatal("no empty state")
	}
}

func TestShutdownSavesAndCleansUp(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelProgram, "HDMI-A-1", true)
	h.m.Detach(page.PanelCleanFeed, "DP-3", true)
	unsubs := len(h.m.wins)
	if unsubs != 2 {
		t.Fatal("windows")
	}
	h.m.Shutdown()
	if len(h.m.Windows()) != 0 || h.disp.forgot < 2 {
		t.Fatalf("windows left %v forgot %d", h.m.Windows(), h.disp.forgot)
	}
	for _, p := range built {
		if !p.closed {
			t.Fatalf("panel %s still open", p.id)
		}
	}
	l, err := layout.Load("", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(l.Windows); !reflect.DeepEqual(got, []string{"main@DP-2:maximized", "program@HDMI-A-1:fullscreen", "cleanfeed@DP-3:fullscreen"}) {
		t.Fatalf("saved %v", got)
	}
	// After shutdown nothing reacts any more.
	h.host.setActive(&page.Context{})
	h.disp.setMonitors(userMonitors()[:1])
	h.m.Detach(page.PanelProgram, "", false)
	if len(h.m.Windows()) != 0 {
		t.Fatal("opened after shutdown")
	}
	h.m.Shutdown() // twice is fine
}

func TestShutdownWithoutSaveOnQuit(t *testing.T) {
	h := newHarness(t, true)
	h.host.reg.Run("workspace.saveOnQuit")
	if settings.Get().Display.SaveLayoutOnQuit {
		t.Fatal("toggle")
	}
	if c, _ := h.host.reg.Get("workspace.saveOnQuit"); !c.Toggle || c.Checked {
		t.Fatalf("command %+v", c)
	}
	h.m.Shutdown()
	if _, err := layout.Load("", true); err == nil {
		t.Fatal("saved although disabled")
	}
}

func TestUserClosingAWindowOnlyRemovesIt(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelProgram, "HDMI-A-1", true)
	h.m.Detach(page.PanelScopes, "DP-3", false)
	h.m.Window(page.PanelScopes).Close() // title bar ×
	if got := h.m.Windows(); !reflect.DeepEqual(got, []string{page.PanelProgram}) {
		t.Fatalf("windows %v", got)
	}
	if !lastPanel(page.PanelScopes).closed || lastPanel(page.PanelProgram).closed {
		t.Fatal("wrong panel closed")
	}
	if got := ids(h.m.Current().Windows); len(got) != 2 {
		t.Fatalf("layout %v", got)
	}
}

func TestRestoreUsesCurrentOrNamedLayout(t *testing.T) {
	h := newHarness(t, true)
	cur := layout.Default()
	cur.Name = "Abend"
	cur.Windows = append(cur.Windows, layout.Window{ID: "program", Panel: page.PanelProgram, Monitor: "HDMI-A-1", Mode: layout.ModeFullscreen})
	if err := layout.Save(cur, true); err != nil {
		t.Fatal(err)
	}
	named := layout.Default()
	named.Name = "Farbe"
	named.Windows = append(named.Windows, layout.Window{ID: "scopes", Panel: page.PanelScopes, Monitor: "DP-3", Mode: layout.ModeMaximized})
	if err := layout.Save(named, false); err != nil {
		t.Fatal(err)
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.Layout = "Farbe" })
	h.m.Restore()
	if got := h.m.Windows(); !reflect.DeepEqual(got, []string{page.PanelProgram}) || h.m.LayoutName() != "Abend" {
		t.Fatalf("with save on quit: %v %s", got, h.m.LayoutName())
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.SaveLayoutOnQuit = false })
	h.m.Restore()
	if got := h.m.Windows(); !reflect.DeepEqual(got, []string{page.PanelScopes}) || h.m.LayoutName() != "Farbe" {
		t.Fatalf("named: %v %s", got, h.m.LayoutName())
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.Layout = "Gibt es nicht" })
	h.m.Restore()
	if len(h.m.Windows()) != 0 {
		t.Fatal("missing layout must fall back to the default")
	}
}

func TestDetachToggleUsesBestOtherMonitor(t *testing.T) {
	h := newHarness(t, true)
	h.host.reg.Run("workspace.detach.cleanfeed")
	if s := h.state(page.PanelCleanFeed); s.mon != "DP-3" || s.mode != layout.ModeFullscreen {
		t.Fatalf("clean feed %+v", s)
	}
	h.host.reg.Run("workspace.detach.timeline")
	if s := h.state(page.PanelTimeline); s.mon != "DP-3" || s.mode != layout.ModeMaximized {
		t.Fatalf("timeline %+v", s)
	}
	if c, _ := h.host.reg.Get("workspace.detach.timeline"); !c.Checked || !c.Enabled {
		t.Fatalf("command %+v", c)
	}
	h.host.reg.Run("workspace.detach.timeline")
	if h.m.Window(page.PanelTimeline) != nil {
		t.Fatal("toggle did not close")
	}
	if c, _ := h.host.reg.Get("workspace.detach.queue"); c.Enabled {
		t.Fatal("unregistered panel must be disabled")
	}
	// With one monitor the panel opens as a window on it.
	h.disp.ms = userMonitors()[:1]
	h.host.reg.Run("workspace.detach.scopes")
	if s := h.state(page.PanelScopes); s.mon != "DP-2" || s.mode != layout.ModeWindow || s.r.W != 1280 {
		t.Fatalf("single monitor %+v", s)
	}
	h.host.reg.Run("workspace.closeDetached")
	if len(h.m.Windows()) != 0 {
		t.Fatal("close all")
	}
	if c, _ := h.host.reg.Get("workspace.closeDetached"); c.Enabled {
		t.Fatal("close all enabled without windows")
	}
}

func TestAllWorkspaceCommandsAreBound(t *testing.T) {
	h := newHarness(t, true)
	for _, in := range command.Catalog {
		if !strings.HasPrefix(in.ID, "workspace.") {
			continue
		}
		if c, _ := h.host.reg.Get(in.ID); !c.Available {
			t.Errorf("%s not bound", in.ID)
		}
	}
}

func TestDynamicMenus(t *testing.T) {
	h := newHarness(t, true)
	reg := h.host.reg
	mons := reg.DynamicItems(command.DynMonitors)
	if len(mons) != 3 || mons[0].Submenu == nil || !strings.HasPrefix(mons[0].Label, "DP-2") {
		t.Fatalf("monitors %+v", mons)
	}
	// DP-3 ▸ Programm im Vollbild
	var dp3 command.Item
	for _, it := range mons {
		if strings.HasPrefix(it.Label, "DP-3") {
			dp3 = it
		}
	}
	labels := []string{}
	for _, it := range dp3.Submenu.Items {
		labels = append(labels, it.Label)
	}
	if !reflect.DeepEqual(labels, []string{"Programm im Vollbild", "Clean-Feed im Vollbild", "Hauptfenster hierher", "", "Nicht verwenden"}) {
		t.Fatalf("submenu %v", labels)
	}
	dp3.Submenu.Items[0].Run()
	if s := h.state(page.PanelProgram); s.mon != "DP-3" || s.mode != layout.ModeFullscreen {
		t.Fatalf("program %+v", s)
	}
	for _, it := range reg.DynamicItems(command.DynMonitors) {
		if strings.HasPrefix(it.Label, "DP-3") && it.Submenu.Items[0].Checked != 2 {
			t.Fatal("not checked")
		}
	}
	dp3.Submenu.Items[0].Run() // toggles off
	if h.m.Window(page.PanelProgram) != nil {
		t.Fatal("toggle off")
	}
	dp3.Submenu.Items[2].Run() // main window here
	if s := h.disp.state(h.host.main); s.mon != "DP-3" {
		t.Fatalf("main %+v", s)
	}
	dp3.Submenu.Items[4].Run() // do not use DP-3: the main window leaves it
	if !h.m.unused["DP-3"] || h.disp.state(h.host.main).mon == "DP-3" {
		t.Fatal("unused")
	}
	if dp3.Submenu.Items[0].Enabled() {
		t.Fatal("program on unused monitor enabled")
	}

	wins := reg.DynamicItems(command.DynWindows)
	if len(wins) != 1 || wins[0].Enabled == nil || wins[0].Enabled() {
		t.Fatalf("empty windows %+v", wins)
	}
	h.m.Detach(page.PanelScopes, "HDMI-A-1", true)
	wins = reg.DynamicItems(command.DynWindows)
	if len(wins) != 1 || wins[0].Label != "Scopes — HDMI-A-1 (Vollbild)" {
		t.Fatalf("windows %+v", wins)
	}
	wins[0].Submenu.Items[1].Run() // leave fullscreen
	if h.m.Window(page.PanelScopes).FullScreen() {
		t.Fatal("fullscreen toggle")
	}
	wins[0].Submenu.Items[3].Run()
	if h.m.Window(page.PanelScopes) != nil {
		t.Fatal("close from menu")
	}

	if it := reg.DynamicItems(command.DynLayouts); len(it) != 1 || it[0].Enabled() {
		t.Fatalf("no layouts %+v", it)
	}
	if err := h.m.SaveLayout("Schnitt"); err != nil {
		t.Fatal(err)
	}
	if err := h.m.SaveLayout("Abnahme"); err != nil {
		t.Fatal(err)
	}
	it := reg.DynamicItems(command.DynLayouts)
	if len(it) != 2 || it[0].Label != "Abnahme" || it[0].Checked != 2 || it[1].Checked != 1 {
		t.Fatalf("layouts %+v", it)
	}
	it[1].Run()
	if h.m.LayoutName() != "Schnitt" {
		t.Fatal("load from menu")
	}
	if pp := reg.DynamicItems(command.DynPanelPlace); len(pp) != len(page.Panels()) || len(pp[0].Submenu.Items) != 3 {
		t.Fatalf("panel place %+v", pp)
	}
}

func TestLayoutManagement(t *testing.T) {
	h := newHarness(t, true)
	if err := h.m.SaveLayout(" "); err == nil {
		t.Fatal("empty name")
	}
	if err := h.m.SaveLayout("_x"); err == nil {
		t.Fatal("reserved name")
	}
	if err := h.m.SaveLayout("Schnitt"); err != nil {
		t.Fatal(err)
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.Layout = "Schnitt" })
	if err := h.m.RenameLayout("Schnitt", "Schnitt groß"); err != nil {
		t.Fatal(err)
	}
	if got := layout.List(); !reflect.DeepEqual(got, []string{"Schnitt groß"}) {
		t.Fatalf("list %v", got)
	}
	if h.m.LayoutName() != "Schnitt groß" || settings.Get().Display.Layout != "Schnitt groß" {
		t.Fatal("rename did not follow current/default")
	}
	lm := h.m.newLayoutManager()
	lm.build()
	lm.list.Select(0)
	lm.duplicate()
	lm.duplicate()
	if got := layout.List(); !reflect.DeepEqual(got, []string{"Schnitt groß", "Schnitt groß Kopie", "Schnitt groß Kopie 2"}) {
		t.Fatalf("duplicates %v", got)
	}
	lm.list.Select(1)
	lm.makeDefault()
	if settings.Get().Display.Layout != "Schnitt groß Kopie" {
		t.Fatal("as default")
	}
	if err := h.m.RenameLayout("Schnitt groß Kopie", "Schnitt groß"); err == nil {
		t.Fatal("rename onto existing")
	}
	if err := h.m.DeleteLayout("Schnitt groß Kopie 2"); err != nil {
		t.Fatal(err)
	}
	if err := h.m.DeleteLayout("weg"); err == nil {
		t.Fatal("delete missing")
	}
	h.m.LoadLayout("weg")
	if len(h.errs) != 1 || !strings.Contains(h.errs[0].Error(), "„weg“") {
		t.Fatalf("errors %v", h.errs)
	}
	if uniqueName("A", []string{"A", "A 2"}) != "A 3" {
		t.Fatal("unique")
	}
}

func TestResetLayout(t *testing.T) {
	h := newHarness(t, true)
	h.m.Detach(page.PanelProgram, "DP-3", true)
	h.m.Detach(page.PanelScopes, "DP-3", false)
	h.host.reg.Run("workspace.resetLayout")
	if len(h.m.Windows()) != 0 || h.m.LayoutName() != "Standard" {
		t.Fatal("reset")
	}
	if p, ok := h.host.applied[page.Edit]; !ok || len(p.Splits) != 0 {
		t.Fatalf("pages %+v", h.host.applied)
	}
	if s := h.disp.state(h.host.main); s.mode != layout.ModeMaximized {
		t.Fatalf("main %+v", s)
	}
}

func TestKeysInDetachedWindow(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	var ran []string
	h.host.reg.Bind(command.Global,
		command.Binding{ID: "play.toggle", Run: func() { ran = append(ran, "play") }},
		command.Binding{ID: "file.save", Run: func() { ran = append(ran, "save") }},
		command.Binding{ID: "edit.undo", Run: func() { ran = append(ran, "undo") }},
		command.Binding{ID: "view.page.color", Run: func() { ran = append(ran, "color") }},
	)
	h.m.Detach(page.PanelProgram, "DP-3", false)
	w := h.m.Window(page.PanelProgram)
	p := lastPanel(page.PanelProgram)
	p.eat[fyne.KeyJ] = true
	key := w.Canvas().OnTypedKey()
	key(&fyne.KeyEvent{Name: fyne.KeyJ})
	key(&fyne.KeyEvent{Name: fyne.KeySpace})
	if !reflect.DeepEqual(p.keys, []fyne.KeyName{fyne.KeyJ, fyne.KeySpace}) || !reflect.DeepEqual(ran, []string{"play"}) {
		t.Fatalf("keys %v ran %v", p.keys, ran)
	}
	h.m.mods = func() fyne.KeyModifier { return fyne.KeyModifierShift }
	key(&fyne.KeyEvent{Name: fyne.Key3})
	h.m.mods = func() fyne.KeyModifier { return 0 }
	if !slices.Contains(ran, "color") {
		t.Fatalf("Shift+3: %v", ran)
	}
	d := h.m.find(page.PanelProgram)
	sc := shortcutTyper(func(s fyne.Shortcut) {
		if fn := d.handlers[s.ShortcutName()]; fn != nil {
			fn(s)
		}
	})
	sc.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyS, Modifier: fyne.KeyModifierControl})
	sc.TypedShortcut(&fyne.ShortcutUndo{})
	if !slices.Contains(ran, "save") || !slices.Contains(ran, "undo") {
		t.Fatalf("shortcuts: %v", ran)
	}
	// Remapping a command is picked up.
	h.host.reg.SetShortcut("file.save", "Ctrl+Alt+P")
	ran = nil
	sc.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyP, Modifier: fyne.KeyModifierControl | fyne.KeyModifierAlt})
	sc.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyS, Modifier: fyne.KeyModifierControl})
	if !reflect.DeepEqual(ran, []string{"save"}) {
		t.Fatalf("remapped: %v", ran)
	}
	// F11 toggles this window's fullscreen, Esc leaves it when the panel does not use it.
	key(&fyne.KeyEvent{Name: fyne.KeyF11})
	if !w.FullScreen() {
		t.Fatal("F11")
	}
	key(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if w.FullScreen() {
		t.Fatal("Esc")
	}
	// A dialog in the window swallows keys.
	ran = nil
	w.Canvas().Overlays().Add(widget.NewLabel("Dialog"))
	key(&fyne.KeyEvent{Name: fyne.KeySpace})
	if len(ran) != 0 {
		t.Fatal("key passed a modal")
	}
}

func TestCleanFeedFullscreenAndCursor(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelCleanFeed, "DP-3", true)
	d := h.m.find(page.PanelCleanFeed)
	if !d.win.FullScreen() || d.header.Visible() {
		t.Fatal("fullscreen clean feed must hide the header")
	}
	d.idle.MouseIn(&desktop.MouseEvent{})
	if len(h.timer) == 0 {
		t.Fatal("no idle timer")
	}
	h.fireTimers()
	if !h.disp.cursor[d.win] {
		t.Fatal("cursor not hidden after idle")
	}
	d.idle.MouseMoved(&desktop.MouseEvent{})
	if h.disp.cursor[d.win] {
		t.Fatal("cursor not shown on move")
	}
	// A stale timer (moved since) does not hide the cursor, even if it fires anyway.
	h.timer = nil
	d.idle.MouseMoved(&desktop.MouseEvent{})
	d.idle.gen++ // as if another move restarted the timer
	h.timer[0]()
	h.q.Drain()
	if h.disp.cursor[d.win] {
		t.Fatal("stale timer hid the cursor")
	}
	d.win.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if d.win.FullScreen() || !d.header.Visible() {
		t.Fatal("Esc must leave fullscreen and show the header")
	}
	if s := h.state(page.PanelCleanFeed); s.mon != "DP-3" || s.mode != layout.ModeWindow {
		t.Fatalf("after Esc %+v", s)
	}
	d.idle.DoubleTapped(nil)
	if !d.win.FullScreen() {
		t.Fatal("double click")
	}
	h.fireTimers()
	d.close()
	if h.disp.cursor[d.win] {
		t.Fatal("cursor left hidden after close")
	}
	h.fireTimers() // timers after close do nothing
}

func TestHeaderMoveToMonitor(t *testing.T) {
	h := newHarness(t, true)
	h.m.Detach(page.PanelScopes, "DP-3", false)
	d := h.m.find(page.PanelScopes)
	if d.where.Text != "DP-3 · 2560×1440" {
		t.Fatalf("where %q", d.where.Text)
	}
	d.moveTo("HDMI-A-1")
	if s := h.state(page.PanelScopes); s.mon != "HDMI-A-1" || s.mode != layout.ModeMaximized {
		t.Fatalf("moved %+v", s)
	}
	d.toggleFullscreen()
	if s := h.state(page.PanelScopes); s.mon != "HDMI-A-1" || s.mode != layout.ModeFullscreen || d.fsButton.Text != "Vollbild beenden" {
		t.Fatalf("fullscreen %+v", s)
	}
	d.moveTo("DP-2")
	if s := h.state(page.PanelScopes); s.mon != "DP-2" || s.mode != layout.ModeFullscreen {
		t.Fatalf("fullscreen move %+v", s)
	}
	d.showMonitorMenu(d.fsButton) // must not panic
}

func TestWaylandPlacementErrorIsQuiet(t *testing.T) {
	h := newHarness(t, false)
	h.m.Detach(page.PanelScopes, "DP-3", false)
	if len(h.errs) != 0 {
		t.Fatalf("Wayland positioning reported: %v", h.errs)
	}
	h.m.place(h.m.Window(page.PanelScopes), "DP-404", layout.ModeFullscreen, layout.Rect{})
	if len(h.errs) != 1 || !errors.Is(h.errs[0], display.ErrNoMonitor) {
		t.Fatalf("errors %v", h.errs)
	}
	// Detaching the program goes fullscreen: the only way to reach a monitor on Wayland.
	h.host.reg.Run("workspace.detach.program")
	if s := h.state(page.PanelProgram); s.mon != "DP-3" || s.mode != layout.ModeFullscreen {
		t.Fatalf("wayland program %+v", s)
	}
	// Current keeps the monitor the window was put on even though Wayland cannot tell.
	if got := ids(h.m.Current().Windows)[1]; !strings.HasPrefix(got, "scopes@DP-3") {
		t.Fatalf("captured %s", got)
	}
}

// shortcutTyper delivers shortcuts like the window canvas does.
type shortcutTyper func(fyne.Shortcut)

func (f shortcutTyper) TypedShortcut(s fyne.Shortcut) { f(s) }
