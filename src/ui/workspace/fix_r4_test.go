package workspace

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
)

// fire delivers a shortcut to a detached window like its canvas does.
func fire(d *detached, sc fyne.Shortcut) {
	if fn := d.handlers[sc.ShortcutName()]; fn != nil {
		fn(sc)
	}
}

// Umschalt+Entf, Umschalt+Einfg and Strg+Einfg arrive as the driver's Cut, Paste and
// Copy with Secondary set: they must run what those keys are bound to, not Strg+X/V/C.
func TestSecondaryShortcutsInDetachedWindow(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	var ran []string
	bind := func(id string) command.Binding {
		return command.Binding{ID: id, Run: func() { ran = append(ran, id) }}
	}
	h.host.reg.Bind(command.Global, bind("edit.cut"), bind("edit.rippleDelete"), bind("edit.copy"), bind("edit.paste"))
	h.host.reg.SetShortcut("edit.copy", "Ctrl+Insert")
	h.host.reg.SetShortcut("edit.paste", "Shift+Insert")
	h.m.Detach(page.PanelTimeline, "DP-3", false)
	d := h.m.find(page.PanelTimeline)
	fire(d, &fyne.ShortcutCut{Secondary: true})
	fire(d, &fyne.ShortcutCopy{Secondary: true})
	fire(d, &fyne.ShortcutPaste{Secondary: true})
	fire(d, &fyne.ShortcutCut{})
	want := []string{"edit.rippleDelete", "edit.copy", "edit.paste", "edit.cut"}
	if !reflect.DeepEqual(ran, want) {
		t.Fatalf("ran %v, want %v", ran, want)
	}
}

// shortcutPanel is a fake panel that handles key combinations itself.
type shortcutPanel struct {
	*fakePanel
	got []string
}

func (p *shortcutPanel) Shortcut(key fyne.KeyName, mods fyne.KeyModifier) bool {
	p.got = append(p.got, command.Format(key, mods))
	return key == fyne.KeyA
}

// Ctrl chords in a detached window go to the panel first, then to the registry with the
// panel's own page scope, whatever page the main window shows.
func TestDetachedChordsUseThePanelsPage(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	var sp *shortcutPanel
	info, _ := page.PanelByID(page.PanelTimeline)
	old := info.New
	info.New = func(ctx *page.Context) page.Panel {
		sp = &shortcutPanel{fakePanel: old(ctx).(*fakePanel)}
		return sp
	}
	page.RegisterPanel(info)
	t.Cleanup(func() { info.New = old; page.RegisterPanel(info) })

	var ran []string
	reg := h.host.reg
	reg.Bind(command.PageScope(page.Edit), command.Binding{ID: "edit.duplicate", Run: func() { ran = append(ran, "edit") }})
	reg.Bind(command.PageScope(page.Color), command.Binding{ID: "edit.duplicate", Run: func() { ran = append(ran, "color") }})
	reg.SetScopeActive(command.PageScope(page.Color), true) // the main window shows Farbe

	h.m.Detach(page.PanelTimeline, "DP-3", false)
	d := h.m.find(page.PanelTimeline)
	fire(d, &fyne.ShortcutSelectAll{})
	if !reflect.DeepEqual(sp.got, []string{"Ctrl+A"}) {
		t.Fatalf("panel saw %v", sp.got)
	}
	fire(d, &desktop.CustomShortcut{KeyName: fyne.KeyD, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift})
	if !reflect.DeepEqual(ran, []string{"edit"}) {
		t.Fatalf("Strg+Umschalt+D ran %v, want the edit page's duplicate", ran)
	}
	// The main window is unaffected.
	if !reg.Dispatch(fyne.KeyD, fyne.KeyModifierControl|fyne.KeyModifierShift) || ran[1] != "color" {
		t.Fatalf("main window dispatch ran %v", ran)
	}
}

// Names that share a file ("Schnitt/Farbe", "Schnitt_Farbe") are one layout: renaming
// between them keeps it, saving the other asks first.
func TestLayoutNamesSharingAFile(t *testing.T) {
	h := newHarness(t, true)
	if err := h.m.SaveLayout("Schnitt/Farbe"); err != nil {
		t.Fatal(err)
	}
	if err := h.m.RenameLayout("Schnitt/Farbe", "Schnitt_Farbe"); err != nil {
		t.Fatal(err)
	}
	if got := layout.List(); !reflect.DeepEqual(got, []string{"Schnitt_Farbe"}) {
		t.Fatalf("after rename %v", got)
	}
	if err := h.m.SaveLayout("Zwei"); err != nil {
		t.Fatal(err)
	}
	if err := h.m.RenameLayout("Zwei", "Schnitt/Farbe"); err == nil || !strings.Contains(err.Error(), "Schnitt_Farbe") {
		t.Fatalf("rename onto the other layout's file: %v", err)
	}
	if layoutWithFile("Schnitt/Farbe") != "Schnitt_Farbe" {
		t.Fatal("the save dialog would not ask before replacing")
	}
	if got := uniqueName("Schnitt/Farbe", layout.List()); got != "Schnitt/Farbe 2" {
		t.Fatalf("unique name %q", got)
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.Layout = "Zwei" })
	if err := h.m.DeleteLayout("Zwei"); err != nil {
		t.Fatal(err)
	}
	if settings.Get().Display.Layout != "Standard" {
		t.Fatalf("start layout still %q after deleting it", settings.Get().Display.Layout)
	}
}

// Wayland cannot maximize on a chosen output: panels for another monitor go fullscreen
// there, and "Hauptfenster hierher" is off for a maximized main window.
func TestWaylandOnlyPromisesWhatItCanDo(t *testing.T) {
	h := newHarness(t, false)
	h.host.ctx = &page.Context{}
	h.m.ToggleDetached(page.PanelTimeline)
	d := h.m.find(page.PanelTimeline)
	if s := h.state(page.PanelTimeline); s.mode != layout.ModeFullscreen || s.mon != "DP-3" {
		t.Fatalf("timeline %+v", s)
	}
	if strings.Contains(d.where.Text, "Maximiert") {
		t.Fatalf("header claims %q", d.where.Text)
	}
	if h.m.canMoveMain() {
		t.Fatal("Hauptfenster hierher offered for a maximized main window on Wayland")
	}
	h.m.moveMain("DP-3")
	if h.m.mainMonitor() == "DP-3" {
		t.Fatal("main monitor recorded although the window cannot move")
	}
	// A windowed panel on another monitor says where to drag it instead of claiming it.
	h.m.Detach(page.PanelScopes, "HDMI-A-1", false)
	if w := h.m.find(page.PanelScopes).where.Text; !strings.Contains(w, "selbst auf HDMI-A-1 ziehen") {
		t.Fatalf("scopes header %q", w)
	}
	// The clean feed then picks a monitor other than the main window's.
	h.m.ToggleDetached(page.PanelCleanFeed)
	if s := h.state(page.PanelCleanFeed); s.mon == "DP-2" {
		t.Fatal("clean feed covers the main window")
	}
}

// Anwenden in the monitor dialog keeps windows that wait for an unplugged monitor.
func TestDialogKeepsWaitingWindows(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	l := layout.Default()
	l.Main().Monitor = "DP-2"
	l.Windows = append(l.Windows, layout.Window{ID: "cleanfeed", Panel: page.PanelCleanFeed, Monitor: "DP-9", Mode: layout.ModeFullscreen})
	h.m.Apply(l)
	md := openDialog(t, h)
	md.applyPlan()
	if got := ids(h.m.Current().Windows); !slices.Contains(got, "cleanfeed@DP-9:fullscreen") {
		t.Fatalf("after Anwenden %v", got)
	}
	if h.m.find(page.PanelCleanFeed).fallbackFrom != "DP-9" {
		t.Fatal("fallback forgotten")
	}
}

// An explicit Vollbild on a fallback window replaces the remembered monitor.
func TestFullscreenOnFallbackIsExplicit(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelProgram, "HDMI-A-1", false)
	h.disp.setMonitors(userMonitors()[:2]) // HDMI-A-1 unplugged
	d := h.m.find(page.PanelProgram)
	if d.fallbackFrom != "HDMI-A-1" {
		t.Fatal("no fallback")
	}
	d.toggleFullscreen()
	if d.fallbackFrom != "" || strings.Contains(d.where.Text, "Ersatz") {
		t.Fatalf("fallback kept: %q", d.where.Text)
	}
	if got := ids(h.m.Current().Windows)[1]; got != "program@DP-2:fullscreen" {
		t.Fatalf("saved %s", got)
	}
	h.disp.setMonitors(userMonitors())
	if s := h.state(page.PanelProgram); s.mon != "DP-2" || s.mode != layout.ModeFullscreen {
		t.Fatalf("replug undid the choice: %+v", s)
	}
}

// "Nicht verwenden" works like unplugging: the windows come back when it is undone.
func TestUnusedMonitorRemembersWindows(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelCleanFeed, "HDMI-A-1", true)
	h.m.setUnused("HDMI-A-1", true)
	if s := h.state(page.PanelCleanFeed); s.mon == "HDMI-A-1" {
		t.Fatal("still on the unused monitor")
	}
	if got := ids(h.m.Current().Windows)[1]; got != "cleanfeed@HDMI-A-1:fullscreen" {
		t.Fatalf("saved %s", got)
	}
	h.m.setUnused("HDMI-A-1", false)
	if s := h.state(page.PanelCleanFeed); s.mon != "HDMI-A-1" || s.mode != layout.ModeFullscreen {
		t.Fatalf("not back: %+v", s)
	}
}

// Detached scopes and mixer keep a usable minimum size.
func TestDetachedMinimumSizes(t *testing.T) {
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	for _, id := range []string{page.PanelScopes, page.PanelMixer} {
		h.m.Detach(id, "DP-3", false)
		ms := h.m.Window(id).Content().MinSize()
		if ms.Width < 420 || ms.Height < 300 {
			t.Fatalf("%s min size %v", id, ms)
		}
	}
}
