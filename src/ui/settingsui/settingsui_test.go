package settingsui

import (
	"encoding/json"
	"framestudio-proto/src/ui/winchrome"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/layout"
	studiotheme "framestudio-proto/src/ui/theme"
)

type fixture struct {
	t   *testing.T
	a   fyne.App
	reg *command.Registry
	w   *window
	dir string
}

func newFixture(t *testing.T, category string) *fixture {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if _, err := settings.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = settings.Reset() })
	kit.UseUIQueue(t)
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	stateMu.Lock()
	startup = nil
	stateMu.Unlock()
	savedMon, savedPlat, savedPos := monitorsFunc, platformFunc, canPositionFunc
	monitorsFunc = func() []display.Monitor {
		return []display.Monitor{
			{ID: "DP-2", Name: "DP-2 · 2560×1440", X: 2560, W: 2560, H: 1440, Refresh: 144, Scale: 1, Primary: true},
			{ID: "DP-3", Name: "DP-3 · 2560×1440", X: 0, W: 2560, H: 1440, Refresh: 60, Scale: 1},
			{ID: "HDMI-A-1", Name: "HDMI-A-1 · 2560×1440", X: 5120, W: 2560, H: 1440, Refresh: 60, Scale: 1},
		}
	}
	platformFunc = func() string { return "wayland" }
	canPositionFunc = func() bool { return false }
	t.Cleanup(func() { monitorsFunc, platformFunc, canPositionFunc = savedMon, savedPlat, savedPos })
	reg := command.NewRegistry()
	Show(a, nil, reg, category)
	w := current
	if w == nil {
		t.Fatal("no window")
	}
	w.confirm = func(_, _, _ string, yes func()) { yes() }
	w.win.Resize(fyne.NewSize(winW, winH))
	t.Cleanup(func() { Close(); Restart = nil })
	return &fixture{t: t, a: a, reg: reg, w: w, dir: dir}
}

func (f *fixture) field(label string) *field {
	f.t.Helper()
	for _, fl := range f.w.fields {
		if fl.label == label {
			return fl
		}
	}
	f.t.Fatalf("no field %q", label)
	return nil
}

func (f *fixture) saved() settings.Settings {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "framestudio", "settings.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var s settings.Settings
	if err := json.Unmarshal(b, &s); err != nil {
		f.t.Fatal(err)
	}
	return s
}

func TestEveryCategoryBuilds(t *testing.T) {
	f := newFixture(t, "")
	if f.w.cur.id != catGeneral {
		t.Fatalf("default category %q", f.w.cur.id)
	}
	for _, id := range Categories() {
		f.w.showCategory(id)
		if f.w.cur.id != id || len(f.w.body.Objects) != 1 {
			t.Fatalf("category %s not shown", id)
		}
		if f.w.cur.full == nil && len(f.w.cur.fields) == 0 {
			t.Fatalf("category %s has no settings", id)
		}
	}
	f.w.showCategory("Darstellung")
	if f.w.cur.id != catDisplay {
		t.Fatal("categories open by German title too")
	}
	// Every settings field has a control (Keymap is the editor).
	labels := map[string]bool{}
	for _, fl := range f.w.fields {
		if labels[fl.label] {
			t.Fatalf("duplicate label %q", fl.label)
		}
		labels[fl.label] = true
	}
	if len(f.w.fields) < 35 {
		t.Fatalf("only %d settings", len(f.w.fields))
	}
	// A second Show reuses the window and switches the category.
	Show(f.a, nil, f.reg, "audio")
	if current != f.w || f.w.cur.id != catAudio {
		t.Fatal("Show again should focus the open window on Audio")
	}
}

func TestEveryFieldResetsToDefault(t *testing.T) {
	f := newFixture(t, "")
	d := settings.Defaults()
	for _, fl := range f.w.fields {
		s := settings.Get()
		fl.reset(&s, d)
	}
	// Changing every field to something else and resetting each restores the defaults.
	_ = settings.Update(func(s *settings.Settings) {
		s.General = settings.General{Startup: "new", AutosaveSeconds: 5, ProjectsDir: "/x", DefaultTemplate: "720p25"}
		s.Playback = settings.Playback{PreviewQuality: "half", MaxPreviewH: 360, Follow: "off", LoopDefault: true, SafeArea: true}
		s.Audio = settings.Audio{Player: "ffplay", LatencyMS: 3, Muted: true, MasterGain: 0.2}
		s.Timeline = settings.Timeline{StillSeconds: 1, GeneratorSecs: 1, FadeSeconds: 2, VideoTrackH: 99, AudioTrackH: 99}
		s.Media = settings.Media{FrameCacheMB: 1, StillCacheMB: 1, AudioCacheMB: 1, ImportRecursive: true, MaxDecoders: 9}
		s.Export = settings.Export{Folder: "/y", Preset: "GIF 480p"}
		s.Display = settings.Display{Platform: "x11", Scale: 2, Accent: "teal", Compact: true, Layout: "Schnitt"}
	})
	for _, c := range f.w.cats {
		f.w.resetCategory(c)
	}
	got := settings.Get()
	got.General.LastOpen = nil
	want := settings.Defaults()
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Fatalf("after resetting every category:\n got %s\nwant %s", gb, wb)
	}
}

func TestControlsUpdateSettingsLive(t *testing.T) {
	f := newFixture(t, "audio")

	scrub := f.field("Ton beim Scrubben").input.(*widget.Check)
	if !scrub.Checked {
		t.Fatal("scrub audio is on by default")
	}
	test.Tap(scrub)
	if settings.Get().Audio.ScrubAudio || f.saved().Audio.ScrubAudio {
		t.Fatal("unchecking must apply and persist at once")
	}

	player := f.field("Audioausgabe").input.(*widget.Select)
	for _, o := range player.Options {
		if strings.HasPrefix(o, "PulseAudio") {
			player.SetSelected(o)
		}
	}
	if settings.Get().Audio.Player != "paplay" {
		t.Fatalf("player %q", settings.Get().Audio.Player)
	}

	lat := f.field("Latenzausgleich")
	e := lat.input.(*commitEntry)
	e.SetText("")
	test.Type(e, "abc")
	if lat.errText.Hidden || settings.Get().Audio.LatencyMS != 0 {
		t.Fatal("invalid input shows a message and changes nothing")
	}
	e.SetText("")
	test.Type(e, "5000")
	if lat.errText.Hidden || !strings.Contains(lat.errText.Text, "0 bis 1000") {
		t.Fatalf("out of range: %q", lat.errText.Text)
	}
	e.SetText("120")
	if !lat.errText.Hidden || settings.Get().Audio.LatencyMS != 120 {
		t.Fatalf("valid input applies: %d", settings.Get().Audio.LatencyMS)
	}

	gain := f.field("Abhörlautstärke").input.(*kit.Slider)
	gain.SetValue(50)
	if settings.Get().Audio.MasterGain != 0.5 {
		t.Fatalf("gain %v", settings.Get().Audio.MasterGain)
	}

	// Decimal comma in a float field.
	still := f.field("Dauer von Standbildern").input.(*commitEntry)
	still.SetText("2,5")
	if settings.Get().Timeline.StillSeconds != 2.5 {
		t.Fatalf("still seconds %v", settings.Get().Timeline.StillSeconds)
	}

	// Folder chooser.
	var asked string
	f.w.pickFolder = func(title, start string, done func(string)) { asked = title; done("/srv/projekte") }
	test.Tap(f.field("Projektordner").input.(*widget.Button))
	if asked == "" || settings.Get().General.ProjectsDir != "/srv/projekte" {
		t.Fatal("folder choice applies")
	}
	f.w.pickFolder = func(_, _ string, done func(string)) { done("") }
	test.Tap(f.field("Projektordner").input.(*widget.Button))
	if settings.Get().General.ProjectsDir != "/srv/projekte" {
		t.Fatal("cancelling keeps the folder")
	}

	// Accent swatches.
	sws := f.field("Akzentfarbe").input.([]*kit.Swatch)
	test.Tap(sws[1])
	if settings.Get().Display.Accent != studiotheme.Accents[1].ID {
		t.Fatalf("accent %q", settings.Get().Display.Accent)
	}

	// Choices with non-string values.
	h := f.field("Höchste Vorschauhöhe").input.(*widget.Select)
	h.SetSelected("1080 Pixel")
	if settings.Get().Playback.MaxPreviewH != 1080 {
		t.Fatal("preview height")
	}
}

func TestExternalChangesSyncControls(t *testing.T) {
	f := newFixture(t, "audio")
	muted := f.field("Vorschau stummschalten").input.(*widget.Check)
	_ = settings.Update(func(s *settings.Settings) {
		s.Audio.Muted = true
		s.Audio.LatencyMS = 77
		s.Media.MaxDecoders = 6
	})
	if !muted.Checked {
		t.Fatal("check follows the settings")
	}
	if e := f.field("Latenzausgleich").input.(*commitEntry); e.Text != "77" {
		t.Fatalf("entry %q", e.Text)
	}
	if sl := f.field("Gleichzeitige Decoder").input.(*kit.Slider); sl.Value != 6 {
		t.Fatalf("slider %v", sl.Value)
	}
	// Syncing must not write back (no loop): the decoders stay where they were set.
	if settings.Get().Media.MaxDecoders != 6 {
		t.Fatal("sync changed settings")
	}
}

func TestSearchFiltersByLabel(t *testing.T) {
	f := newFixture(t, "general")
	f.w.search.SetText("latenz")
	if f.w.query != "latenz" {
		t.Fatal("query")
	}
	var audioCat, general *category
	for _, c := range f.w.cats {
		switch c.id {
		case catAudio:
			audioCat = c
		case catGeneral:
			general = c
		}
	}
	if audioCat.item.hits != 1 || audioCat.item.dimmed {
		t.Fatalf("audio hits %d", audioCat.item.hits)
	}
	if !general.item.dimmed {
		t.Fatal("categories without hits are dimmed")
	}
	if !contains(f.w.body, f.field("Latenzausgleich").row) || contains(f.w.body, f.field("Beim Start").row) {
		t.Fatal("only matching rows are shown")
	}

	f.w.search.SetText("Cache")
	if !contains(f.w.body, f.field("Audiospeicher").row) {
		t.Fatal("a category title match shows its settings")
	}

	f.w.search.SetText("gibtsnicht")
	if !containsText(f.w.body, "Keine Einstellung gefunden") {
		t.Fatal("no-result message")
	}

	f.w.win.Canvas().Unfocus()
	f.w.win.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if f.w.query != "" || f.w.search.Text != "" {
		t.Fatal("Esc clears the search")
	}
	if !contains(f.w.body, f.field("Beim Start").row) {
		t.Fatal("back to the category")
	}
	f.w.search.SetText("tasten")
	f.w.selectCategory(f.w.cats[len(f.w.cats)-1])
	if f.w.query != "" || f.w.cur.id != catKeymap {
		t.Fatal("picking a category ends the search")
	}
}

func TestResetCategoryAndAll(t *testing.T) {
	f := newFixture(t, "audio")
	_ = settings.Update(func(s *settings.Settings) {
		s.Audio.Muted = true
		s.Audio.Player = "ffplay"
		s.Timeline.Snapping = false
		s.Keymap["file.save"] = "Ctrl+Alt+S"
	})
	f.w.askResetCategory(f.w.cur)
	s := settings.Get()
	if s.Audio.Muted || s.Audio.Player != "auto" {
		t.Fatal("audio reset")
	}
	if s.Timeline.Snapping {
		t.Fatal("other categories stay")
	}
	f.w.askResetAll()
	s = settings.Get()
	if !s.Timeline.Snapping || len(s.Keymap) != 0 {
		t.Fatal("reset all")
	}
	if !f.saved().Timeline.Snapping {
		t.Fatal("reset is saved")
	}
	// The confirmation can be declined.
	_ = settings.Update(func(s *settings.Settings) { s.Audio.Muted = true })
	f.w.confirm = func(_, _, _ string, yes func()) {}
	f.w.askResetAll()
	if !settings.Get().Audio.Muted {
		t.Fatal("declined reset changes nothing")
	}
}

func TestRestartBadgeAndBanner(t *testing.T) {
	f := newFixture(t, "monitors")
	if !f.field("Fenstersystem").restart || !f.field("Skalierung").restart || f.field("Akzentfarbe").restart {
		t.Fatal("restart badges on platform and scale only")
	}
	if f.w.banner.Visible() {
		t.Fatal("no banner without changes")
	}
	f.field("Fenstersystem").input.(*widget.Select).SetSelected("X11 (XWayland)")
	if settings.Get().Display.Platform != "x11" {
		t.Fatal("platform")
	}
	if !f.w.banner.Visible() || f.w.restartBtn.Visible() {
		t.Fatal("banner without restart button while the shell offers no restart")
	}
	restarted := false
	Restart = func() { restarted = true }
	f.field("Skalierung").input.(*widget.Select).SetSelected("System × 1,5")
	if !f.w.restartBtn.Visible() || !strings.Contains(f.w.bannerText.Text, "Fenstersystem und Skalierung") {
		t.Fatalf("banner %q", f.w.bannerText.Text)
	}
	test.Tap(f.w.restartBtn)
	if !restarted || current != nil {
		t.Fatal("Jetzt neu starten closes the window and calls the hook")
	}
}

func TestMonitorsCategory(t *testing.T) {
	f := newFixture(t, "monitors")
	ran := 0
	f.reg.Bind(command.Global, command.Binding{ID: "workspace.monitors", Run: func() { ran++ }})
	f.w.showCategory("monitors")
	ov := f.w.cur
	if ov.extraObj == nil {
		t.Fatal("overview")
	}
	if !containsText(ov.extraObj, "Wayland") || !containsText(ov.extraObj, "Nur im Vollbild") ||
		!containsText(ov.extraObj, "HDMI-A-1") || !containsText(ov.extraObj, "Hauptmonitor") {
		t.Fatal("platform, placement and monitor list are shown")
	}
	var open *widget.Button
	walk(ov.extraObj, func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok && strings.HasPrefix(b.Text, "Monitore und Fenster") {
			open = b
		}
	})
	if open == nil || open.Disabled() {
		t.Fatal("button enabled once the command is bound")
	}
	test.Tap(open)
	if ran != 1 {
		t.Fatal("button runs workspace.monitors")
	}
}

func TestCloseUnsubscribes(t *testing.T) {
	f := newFixture(t, "audio")
	Close()
	if current != nil || !f.w.closed {
		t.Fatal("closed")
	}
	_ = settings.Update(func(s *settings.Settings) { s.Audio.Muted = true }) // must not touch the closed window
	if f.field("Vorschau stummschalten").input.(*widget.Check).Checked {
		t.Fatal("closed window is no longer synced")
	}
}

func TestStartupErrorAndRestartFlags(t *testing.T) {
	SetStartupError(os.ErrPermission)
	if TakeStartupError() != os.ErrPermission || TakeStartupError() != nil {
		t.Fatal("startup error is taken once")
	}
	RequestRestart()
	if !RestartRequested() {
		t.Fatal("restart requested")
	}
	CancelRestart()
	if RestartRequested() {
		t.Fatal("cancelled")
	}
	if TemplateByID("1080P60").FPS != 60 || TemplateByID("nope").ID != Templates[0].ID {
		t.Fatal("templates")
	}
}

func TestFormatting(t *testing.T) {
	cases := map[string]string{mb(512): "512 MB", mb(1536): "1,5 GB", mb(8192): "8 GB",
		formatGain(100): "100 % · 0,0 dB", formatGain(50): "50 % · −6,0 dB", formatGain(0): "0 % · −∞ dB",
		formatNum(2.5, 2): "2,5", formatNum(90, 0): "90"}
	for got, want := range cases {
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
}

func walk(o fyne.CanvasObject, fn func(fyne.CanvasObject)) {
	fn(o)
	switch x := o.(type) {
	case *fyne.Container:
		for _, c := range x.Objects {
			walk(c, fn)
		}
	case *container.Scroll:
		walk(x.Content, fn)
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(x).Objects() {
			walk(c, fn)
		}
	}
}

func contains(root, target fyne.CanvasObject) bool {
	found := false
	walk(root, func(o fyne.CanvasObject) {
		if o == target {
			found = true
		}
	})
	return found
}

func containsText(root fyne.CanvasObject, s string) bool {
	found := false
	walk(root, func(o fyne.CanvasObject) {
		switch x := o.(type) {
		case *canvas.Text:
			found = found || strings.Contains(x.Text, s)
		case *widget.Label:
			found = found || strings.Contains(x.Text, s)
		}
	})
	return found
}

func TestScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-r3-settings*.png")
	}
	winchrome.UseFake(t)
	f := newFixture(t, "audio")
	_ = settings.Update(func(s *settings.Settings) { s.Audio.LatencyMS = 40 })
	capture(t, f.w.win, "/tmp/fs-r3-settings-audio.png")
	f.w.showCategory("display")
	_ = settings.Update(func(s *settings.Settings) { s.Display.Platform = "x11" })
	Restart = func() {}
	f.w.load(settings.Get())
	capture(t, f.w.win, "/tmp/fs-r3-settings-display.png")
	capture(t, f.w.win, "/tmp/fs-int-chrome-settings.png")
	f.w.showCategory("monitors")
	capture(t, f.w.win, "/tmp/fs-r3-settings-monitors.png")
	f.w.search.SetText("spur")
	capture(t, f.w.win, "/tmp/fs-r3-settings-search.png")
	f.w.showCategory("general")
	_ = settings.Update(func(s *settings.Settings) { s.General.ProjectsDir = "/home/nutzer/Videos/Frame Studio/Projekte" })
	capture(t, f.w.win, "/tmp/fs-r3-settings-general.png")
}

func capture(t *testing.T, w fyne.Window, path string) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}

// Autosave takes 0 or 10…3600 seconds and saves when the field is left, not per key.
func TestAutosaveFieldRangeAndCommit(t *testing.T) {
	f := newFixture(t, "")
	fl := f.field("Automatisch speichern alle")
	e := fl.input.(*commitEntry)
	writes := 0
	unsub := settings.Subscribe(func(settings.Settings) { writes++ })
	defer unsub()
	e.SetText("5")
	if fl.errText.Hidden || !strings.Contains(fl.errText.Text, "0 oder 10 bis 3600") {
		t.Fatalf("5 s accepted: %q", fl.errText.Text)
	}
	e.FocusLost()
	if settings.Get().General.AutosaveSeconds == 5 {
		t.Fatal("5 s saved")
	}
	e.SetText("")
	test.Type(e, "120")
	if writes != 0 {
		t.Fatalf("saved while typing (%d writes)", writes)
	}
	e.FocusLost()
	if settings.Get().General.AutosaveSeconds != 120 || writes != 1 {
		t.Fatalf("after leaving: %d s, %d writes", settings.Get().General.AutosaveSeconds, writes)
	}
	e.SetText("0")
	e.OnSubmitted(e.Text)
	if settings.Get().General.AutosaveSeconds != 0 {
		t.Fatal("0 (off) not accepted on Enter")
	}
}

// "Layout beim Start" lists the layouts saved while the window is open and keeps a
// value whose file is missing instead of showing "Eigener Wert".
func TestStartLayoutChoicesFollowLayouts(t *testing.T) {
	f := newFixture(t, "")
	sel := f.field("Layout beim Start").input.(*widget.Select)
	l := layout.Default()
	l.Name = "Drei Monitore"
	if err := layout.Save(l, false); err != nil {
		t.Fatal(err)
	}
	f.w.showCategory(catMonitors)
	if !slices.Contains(sel.Options, "Drei Monitore") {
		t.Fatalf("options %v", sel.Options)
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.Layout = "Umbenannt" })
	if sel.Selected != "Umbenannt" {
		t.Fatalf("selected %q (placeholder shown)", sel.Selected)
	}
	sel.SetSelected("Drei Monitore")
	if settings.Get().Display.Layout != "Drei Monitore" {
		t.Fatal("choice not saved")
	}
}

// A restart-only setting decided by the environment shows that instead of the banner.
func TestEnvOverrideInsteadOfRestartBanner(t *testing.T) {
	SetEnvOverrides(map[string]string{"Skalierung": "FYNE_SCALE=1.5"})
	t.Cleanup(func() { SetEnvOverrides(nil) })
	f := newFixture(t, "")
	f.field("Skalierung").input.(*widget.Select).SetSelected("System × 1,2")
	if f.w.banner.Visible() {
		t.Fatal("restart banner promises a change the environment overrides")
	}
	if !strings.Contains(test.RenderObjectToMarkup(f.field("Skalierung").row), "Wird durch FYNE_SCALE=1.5 überschrieben") {
		t.Fatal("row does not name the variable")
	}
}
