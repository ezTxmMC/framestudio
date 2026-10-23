package shell

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// shortcut is one line of the help dialog. Keys lists alternatives separated by " | ";
// each alternative is a chord of caps joined by "+" (the plus key itself is "Plus").
type shortcut struct {
	Keys, Action string
}

// shortcutGroup is a titled block of the help dialog.
type shortcutGroup struct {
	Title string
	Keys  []shortcut
}

// extraShortcuts are the mouse gestures and fixed alternatives outside the registry.
var extraShortcuts = shortcutGroup{"Maus und Alternativen", []shortcut{
	{"Strg+Klick", "Clip zur Auswahl hinzufügen oder entfernen"},
	{"Umschalt+Klick", "Auswahl bis zum Clip erweitern"},
	{"Strg+Mausrad", "Timeline zoomen"},
	{"Ziehen", "Medien aus der Bibliothek in die Timeline ziehen"},
	{"Eingabe", "Ausgewähltes Medium am Playhead einfügen (Schnitt)"},
	{"Rücktaste", "Ausgewählte Clips löschen (wie Entf)"},
	{"Strg+Y", "Wiederholen (alternativ)"},
	{"Alt+1…5 | Strg+1…5", "Seite wechseln (alternativ)"},
	{"Alt | F10", "Menüleiste öffnen"},
}}

// shortcutGroups builds the help content from the registry: every command with a
// shortcut, grouped by category in catalog order, then the extras.
func shortcutGroups(reg *command.Registry) []shortcutGroup {
	var groups []shortcutGroup
	index := map[string]int{}
	for _, c := range reg.All() {
		if c.Shortcut == "" {
			continue
		}
		keys := keySpec(c.Shortcut)
		if keys == "" {
			continue
		}
		i, ok := index[c.Category]
		if !ok {
			i = len(groups)
			index[c.Category] = i
			groups = append(groups, shortcutGroup{Title: c.Category})
		}
		groups[i].Keys = append(groups[i].Keys, shortcut{keys, strings.TrimSuffix(c.Label, "…")})
	}
	return append(groups, extraShortcuts)
}

// keySpec renders a shortcut as German cap names joined by "+" ("Strg+Umschalt+Z"); the
// plus key is "Plus" so the caps split cleanly.
func keySpec(sc string) string {
	k, m, ok := command.Parse(sc)
	if !ok {
		return ""
	}
	key := command.Display(string(k))
	if k == fyne.KeyPlus {
		key = "Plus"
	}
	mods := command.Display(command.Format("X", m))
	return strings.TrimSuffix(mods, "X") + key
}

// keyCapW is the width of the key column of the help dialog.
const keyCapW = 150

func (sh *Shell) showShortcuts() {
	d := dialog.NewCustom("Tastenkürzel", "Schließen", container.NewVScroll(shortcutTable(shortcutGroups(sh.reg))), sh.win)
	d.Resize(fyne.NewSize(1180, 760))
	d.Show()
}

// shortcutTable lays the groups out in three columns of roughly equal height.
func shortcutTable(groups []shortcutGroup) fyne.CanvasObject {
	const ncol = 3
	cols := make([][]shortcutGroup, ncol)
	lines := make([]int, ncol)
	for _, g := range groups {
		best := 0
		for i := range lines {
			if lines[i] < lines[best] {
				best = i
			}
		}
		cols[best] = append(cols[best], g)
		lines[best] += len(g.Keys) + 3
	}
	grid := container.New(layout.NewGridLayoutWithColumns(ncol))
	for _, groups := range cols {
		keyW := float32(keyCapW) // the key column fits the longest chord of the column
		for _, g := range groups {
			for _, k := range g.Keys {
				keyW = max(keyW, container.NewHBox(keyCaps(k.Keys)...).MinSize().Width+12)
			}
		}
		col := container.New(layout.NewCustomPaddedVBoxLayout(2))
		for gi, g := range groups {
			if gi > 0 {
				col.Add(strut(0, 10))
			}
			title := text(strings.ToUpper(g.Title), studiotheme.Muted, 11, true)
			line := canvas.NewRectangle(studiotheme.Border)
			line.SetMinSize(fyne.NewSize(1, 1))
			col.Add(container.NewVBox(title, line, strut(0, 2)))
			for _, k := range g.Keys {
				keys := container.NewStack(strut(keyW, 0), container.NewHBox(keyCaps(k.Keys)...))
				col.Add(container.NewBorder(nil, nil, keys, nil,
					container.NewVBox(layout.NewSpacer(), text(ellipsize(k.Action, 40), studiotheme.Text, 12, false), layout.NewSpacer())))
			}
		}
		grid.Add(container.New(layout.NewCustomPaddedLayout(4, 4, 6, 14), col))
	}
	return grid
}

// keyCaps renders "Strg+Umschalt+Z | Strg+Y" as key caps with separators.
func keyCaps(spec string) []fyne.CanvasObject {
	var out []fyne.CanvasObject
	for ai, alt := range strings.Split(spec, " | ") {
		if ai > 0 {
			out = append(out, centered(text("/", studiotheme.Faint, 11, false)))
		}
		for ki, k := range strings.Split(alt, "+") {
			if ki > 0 {
				out = append(out, centered(text("+", studiotheme.Faint, 10, false)))
			}
			if k == "Plus" {
				k = "+"
			}
			out = append(out, keyCap(k))
		}
	}
	return out
}

// keyCap is one key in a raised mono cap.
func keyCap(label string) fyne.CanvasObject {
	bg := canvas.NewRectangle(studiotheme.Raised)
	bg.CornerRadius = 3
	bg.StrokeColor = studiotheme.Border
	bg.StrokeWidth = 1
	t := kit.MonoText(label, studiotheme.Text, 11)
	minW := strut(18, 0)
	box := container.NewStack(bg, minW, container.New(layout.NewCustomPaddedLayout(2, 2, 6, 6), container.NewCenter(t)))
	return centered(box)
}

func centered(o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(layout.NewSpacer(), o, layout.NewSpacer())
}

// diagnostics lists versions, platform, external tools, monitors, folders and caches.
func (sh *Shell) diagnostics() [][2]string {
	var rows [][2]string
	add := func(k, v string) { rows = append(rows, [2]string{k, v}) }
	add("Frame Studio", appVersion())
	add("Go", runtime.Version()+" · "+runtime.GOOS+"/"+runtime.GOARCH)
	add("Fyne", moduleVersion("fyne.io/fyne/v2"))
	platform := display.Platform()
	if platform == "" {
		platform = sessionType()
	}
	add("Fenstersystem", platform)
	tool := func(name string) string {
		if p, err := exec.LookPath(name); err == nil {
			return "vorhanden (" + p + ")"
		}
		return "fehlt"
	}
	add("ffmpeg", tool("ffmpeg"))
	add("ffprobe", tool("ffprobe"))
	var players []string
	for _, p := range []string{"pw-play", "paplay", "ffplay"} {
		if _, err := exec.LookPath(p); err == nil {
			players = append(players, p)
		}
	}
	if len(players) == 0 {
		add("Tonausgabe", "kein Player gefunden (pw-play, paplay oder ffplay installieren)")
	} else {
		add("Tonausgabe", strings.Join(players, ", "))
	}
	mons := display.Monitors()
	if len(mons) == 0 {
		add("Monitore", "keine Angaben")
	}
	for i, m := range mons {
		v := fmt.Sprintf("%d×%d bei %d,%d", m.W, m.H, m.X, m.Y)
		if m.Refresh > 0 {
			v += fmt.Sprintf(" · %d Hz", m.Refresh)
		}
		if m.Primary {
			v += " · Hauptmonitor"
		}
		add(fmt.Sprintf("Monitor %d: %s", i+1, m.ID), v)
	}
	if dir, err := settings.Dir(); err == nil {
		add("Einstellungsordner", dir)
	} else {
		add("Einstellungsordner", err.Error())
	}
	s := settings.Get()
	add("Cache-Grenzen", fmt.Sprintf("Bilder %d MB · Standbilder %d MB · Ton %d MB · %d Decoder",
		s.Media.FrameCacheMB, s.Media.StillCacheMB, s.Media.AudioCacheMB, s.Media.MaxDecoders))
	nmedia, offline := 0, 0
	for _, p := range sh.projects {
		for _, m := range p.sess.Project.Media {
			nmedia++
			if sh.cache.Offline(m) {
				offline++
			}
		}
	}
	add("Geöffnete Projekte", fmt.Sprintf("%d · %d Medien, davon %d offline", len(sh.projects), nmedia, offline))
	return rows
}

func (sh *Shell) showDiagnostics() {
	rows := sh.diagnostics()
	form := container.New(layout.NewFormLayout())
	var plain strings.Builder
	for _, r := range rows {
		form.Add(text(r[0], studiotheme.Muted, 12, false))
		v := widget.NewLabel(r[1])
		v.Wrapping = fyne.TextWrapWord
		v.Selectable = true
		form.Add(v)
		plain.WriteString(r[0] + ": " + r[1] + "\n")
	}
	var d dialog.Dialog
	copyBtn := widget.NewButton("In Zwischenablage kopieren", func() {
		sh.app.Clipboard().SetContent(plain.String())
		sh.toast.show("Systeminfo kopiert", "Die Angaben liegen in der Zwischenablage.", false)
	})
	closeBtn := widget.NewButton("Schließen", func() { d.Hide() })
	closeBtn.Importance = widget.HighImportance
	d = dialog.NewCustomWithoutButtons("Systeminfo und Diagnose", container.NewVScroll(form), sh.win)
	d.(*dialog.CustomDialog).SetButtons([]fyne.CanvasObject{copyBtn, closeBtn})
	d.Resize(fyne.NewSize(760, 560))
	d.Show()
}

func (sh *Shell) showAbout() {
	ff := "ffmpeg gefunden: Video und Ton werden unterstützt."
	if !media.FFmpegAvailable() {
		ff = "ffmpeg nicht gefunden: Video und Ton lassen sich erst nach Installation von ffmpeg importieren."
	}
	info := widget.NewLabel("Videoschnitt mit Medien-, Schnitt-, Farb-, Ton- und Export-Seite, mehreren Monitoren und eigenen Layouts.\n" + ff)
	info.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(container.NewHBox(wordmarkLarge()),
		text("Version "+appVersion(), studiotheme.Muted, 12, false), container.NewGridWrap(fyne.NewSize(440, 80), info))
	dialog.NewCustom("Über Frame Studio", "Schließen", content, sh.win).Show()
}

func wordmarkLarge() fyne.CanvasObject {
	return text("Frame Studio", studiotheme.Text, 20, true)
}

// appVersion is the module version of the build, or "Prototyp" for development builds.
func appVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "Prototyp"
}

func moduleVersion(path string) string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == path {
				return d.Version
			}
		}
	}
	return "unbekannt"
}

// sessionType is the desktop session type from the environment.
func sessionType() string {
	switch {
	case os.Getenv("WAYLAND_DISPLAY") != "":
		return "wayland"
	case os.Getenv("DISPLAY") != "":
		return "x11"
	}
	if t := os.Getenv("XDG_SESSION_TYPE"); t != "" {
		return t
	}
	return "unbekannt"
}
