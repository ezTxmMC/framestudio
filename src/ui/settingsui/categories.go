package settingsui

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/audio"
	"framestudio-proto/src/export"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/chrome"
	"framestudio-proto/src/ui/keymapui"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/osscale"
)

// buildCategories creates every category and its fields.
func (w *window) buildCategories() []*category {
	general := &category{id: catGeneral, title: "Allgemein", icon: theme.HomeIcon(),
		desc: "Start, Speichern und neue Projekte."}
	playback := &category{id: catPlayback, title: "Wiedergabe", icon: theme.MediaPlayIcon(),
		desc: "Vorschauqualität und wie die Timeline der Wiedergabe folgt."}
	perf := &category{id: catPerf, title: "Vorschau & Leistung", icon: theme.ComputerIcon(),
		desc: "Vorschau-Cache, Proxys und Hardware-Dekodierung. Die Wiedergabe hat immer Vorrang vor Hintergrundarbeit."}
	audioCat := &category{id: catAudio, title: "Audio", icon: theme.VolumeUpIcon(),
		desc: "Tonausgabe der Vorschau. Betrifft nicht den Export."}
	timeline := &category{id: catTimeline, title: "Timeline", icon: iconTimeline,
		desc: "Standarddauern, Spurhöhen und Bearbeitungsverhalten."}
	media := &category{id: catMedia, title: "Medien & Cache", icon: theme.StorageIcon(),
		desc: "Arbeitsspeicher für dekodierte Bilder und Töne, Import."}
	exp := &category{id: catExport, title: "Export", icon: theme.UploadIcon(),
		desc: "Zielordner und Vorgaben für die Ausgabe."}
	display := &category{id: catDisplay, title: "Darstellung", icon: theme.ColorPaletteIcon(),
		desc: "Akzentfarbe, Dichte und sichtbare Leisten."}
	monitors := &category{id: catMonitors, title: "Monitore & Fenster", icon: iconMonitors,
		desc: "Fenstersystem, Bildschirme und Layouts."}
	keymap := &category{id: catKeymap, title: "Tastenkürzel", icon: iconKeyboard,
		desc: "Tastenkürzel aller Befehle ansehen und ändern."}

	// Allgemein
	choice(w, general, "Beim Start", "Was Frame Studio nach dem Öffnen zeigt.", []option[string]{
		{"manager", "Projektverwaltung"}, {"last", "Zuletzt geöffnete Projekte"}, {"new", "Neues leeres Projekt"},
	}, func(s *settings.Settings) *string { return &s.General.Startup })
	autosave := func(s *settings.Settings) *int { return &s.General.AutosaveSeconds }
	w.number(general, "Automatisch speichern alle", "Geänderte Projekte mit Datei werden in diesem Abstand gespeichert: 0 schaltet es aus, sonst 10 bis 3600 Sekunden.",
		"Sekunden", 0, 3600, 0,
		func(s settings.Settings) float64 { return float64(*autosave(&s)) },
		func(s *settings.Settings, v float64) { *autosave(s) = int(v) }, resetter(autosave),
		numberOpts{allowed: func(v float64) bool { return v == 0 || v >= 10 },
			rangeMsg: "Bitte 0 oder 10 bis 3600 Sekunden eingeben.", lazy: true})
	w.folder(general, "Projektordner", "Hier legt Frame Studio neue Projekte ab.", "Standardbibliothek",
		"Projektordner wählen", func(s *settings.Settings) *string { return &s.General.ProjectsDir })
	w.templateChoice(general)
	w.userTemplates(general)
	w.check(general, "Beim Schließen nachfragen", "Vor dem Schließen oder Beenden mit ungespeicherten Änderungen fragen. Aus: Projekte mit Datei werden ohne Rückfrage gespeichert.",
		func(s *settings.Settings) *bool { return &s.General.ConfirmClose })

	// Wiedergabe
	choice(w, playback, "Vorschauqualität", "Geringere Auflösung hält die Wiedergabe bei schweren Projekten flüssig.", []option[string]{
		{"auto", "Automatisch"}, {"full", "Volle Auflösung"}, {"half", "Halbe Auflösung"}, {"quarter", "Viertel-Auflösung"},
	}, func(s *settings.Settings) *string { return &s.Playback.PreviewQuality })
	choice(w, playback, "Höchste Vorschauhöhe", "Obergrenze für die Bildhöhe während der Wiedergabe, unabhängig von der Projektauflösung. Gilt nicht bei „Volle Auflösung“.", []option[int]{
		{0, "Keine Begrenzung"}, {360, "360 Pixel"}, {540, "540 Pixel"}, {720, "720 Pixel"}, {1080, "1080 Pixel"}, {1440, "1440 Pixel"}, {2160, "2160 Pixel"},
	}, func(s *settings.Settings) *int { return &s.Playback.MaxPreviewH })
	choice(w, playback, "Timeline folgt dem Abspielkopf", "Wie die Timeline mitläuft, wenn der Abspielkopf den sichtbaren Bereich verlässt.", []option[string]{
		{"page", "Seitenweise"}, {"smooth", "Fließend"}, {"off", "Nicht folgen"},
	}, func(s *settings.Settings) *string { return &s.Playback.Follow })
	w.check(playback, "Schleife als Standard", "Neu angelegte und geöffnete Projekte starten mit eingeschalteter Schleife.",
		func(s *settings.Settings) *bool { return &s.Playback.LoopDefault })
	w.check(playback, "Sicherheitsrahmen anzeigen", "Title- und Action-Safe-Rahmen im Viewer einblenden.",
		func(s *settings.Settings) *bool { return &s.Playback.SafeArea })

	// Vorschau & Leistung
	choice(w, perf, "Vorschau-Cache", "Speicher für bereits berechnete Bilder der Timeline. Wiederholtes Abspielen derselben Stelle bleibt so auch bei schwerem Material flüssig. Automatisch nutzt etwa 10 % des Arbeitsspeichers.", []option[int]{
		{0, "Automatisch"}, {-1, "Aus"}, {256, "256 MB"}, {512, "512 MB"}, {1024, "1 GB"}, {2048, "2 GB"}, {4096, "4 GB"},
	}, func(s *settings.Settings) *int { return &s.Playback.CacheMB })
	choice(w, perf, "Proxys", "Kleine, schnell lesbare Kopien schwerer Videos für die Vorschau. Automatisch erzeugt sie im Leerlauf für hochauflösendes oder stark komprimiertes Material. Export und Farbbeurteilung nutzen immer das Original.", []option[string]{
		{"auto", "Automatisch"}, {"always", "Für alle Videos"}, {"off", "Aus"},
	}, func(s *settings.Settings) *string { return &s.Playback.ProxyMode })
	choice(w, perf, "Größe des Proxy-Ordners", "Älteste, nicht benutzte Proxys werden gelöscht, sobald der Ordner größer wird.", []option[int]{
		{2, "2 GB"}, {5, "5 GB"}, {10, "10 GB"}, {25, "25 GB"}, {50, "50 GB"}, {100, "100 GB"},
	}, func(s *settings.Settings) *int { return &s.Playback.ProxyCapGB })
	choice(w, perf, "Hardware-Dekodierung", "Nutzt die Grafikeinheit zum Dekodieren, wenn der Treiber das Format sicher beherrscht. Schlägt es fehl, wechselt Frame Studio selbstständig zur Software-Dekodierung. Nie beim Export.", []option[string]{
		{"auto", "Automatisch"}, {"off", "Aus"},
	}, func(s *settings.Settings) *string { return &s.Playback.HwDecode })
	w.check(perf, "Wiedergabe-Statistik anzeigen", "Blendet im Viewer Bildrate, verworfene Bilder, Qualitätsstufe, Cache-Treffer und Dekoder ein. Eine Warnung erscheint auch ohne Statistik, wenn die Wiedergabe überlastet ist.",
		func(s *settings.Settings) *bool { return &s.Playback.Stats })

	// Audio
	players := []option[string]{{"auto", "Automatisch"}}
	names := map[string]string{"pw-play": "PipeWire (pw-play)", "paplay": "PulseAudio (paplay)", "ffplay": "FFmpeg (ffplay)"}
	for _, n := range audio.PlayerNames() {
		label := names[n]
		if label == "" {
			label = n
		}
		if !audio.PlayerInstalled(n) {
			label += " – nicht installiert"
		}
		players = append(players, option[string]{n, label})
	}
	choice(w, audioCat, "Audioausgabe", "Programm, das den Ton der Vorschau abspielt. Ein Wechsel startet die Ausgabe sofort neu.",
		players, func(s *settings.Settings) *string { return &s.Audio.Player })
	w.intNumber(audioCat, "Latenzausgleich", "Gleicht die Verzögerung der Tonausgabe aus, damit Bild und Ton synchron sind. 0 = automatisch geschätzt.",
		"ms", 0, 1000, func(s *settings.Settings) *int { return &s.Audio.LatencyMS })
	w.check(audioCat, "Ton beim Scrubben", "Beim Ziehen oder schrittweisen Bewegen des Abspielkopfs kurz anspielen.",
		func(s *settings.Settings) *bool { return &s.Audio.ScrubAudio })
	w.check(audioCat, "Vorschau stummschalten", "Schaltet nur den Abhörton stumm. Pegelanzeigen und Export bleiben unverändert.",
		func(s *settings.Settings) *bool { return &s.Audio.Muted })
	w.slider(audioCat, "Abhörlautstärke", "Lautstärke der Vorschau. Ändert weder die Mischung noch den Export.",
		0, 200, 5, formatGain,
		func(s settings.Settings) float64 { return math.Round(s.Audio.MasterGain * 100) },
		func(s *settings.Settings, v float64) { s.Audio.MasterGain = v / 100 },
		resetter(func(s *settings.Settings) *float64 { return &s.Audio.MasterGain }))

	// Timeline
	w.check(timeline, "Magnetisch einrasten", "Clips rasten beim Ziehen an Schnitten, Markern und am Abspielkopf ein.",
		func(s *settings.Settings) *bool { return &s.Timeline.Snapping })
	w.floatNumber(timeline, "Dauer von Standbildern", "Länge, mit der Fotos und Grafiken in die Timeline kommen.",
		"Sekunden", 0.1, 3600, func(s *settings.Settings) *float64 { return &s.Timeline.StillSeconds })
	w.floatNumber(timeline, "Dauer von Generatoren", "Länge neuer Titel, Farbflächen und anderer Generatoren.",
		"Sekunden", 0.1, 3600, func(s *settings.Settings) *float64 { return &s.Timeline.GeneratorSecs })
	w.floatNumber(timeline, "Dauer von Blenden", "Standardlänge für Überblendungen und Ein-/Ausblenden.",
		"Sekunden", 0.04, 60, func(s *settings.Settings) *float64 { return &s.Timeline.FadeSeconds })
	w.intSlider(timeline, "Höhe der Videospuren", "", 36, 160, 2, px,
		func(s *settings.Settings) *int { return &s.Timeline.VideoTrackH })
	w.intSlider(timeline, "Höhe der Audiospuren", "", 32, 160, 2, px,
		func(s *settings.Settings) *int { return &s.Timeline.AudioTrackH })
	w.check(timeline, "Filmstreifen anzeigen", "Vorschaubilder in Videoclips zeichnen.",
		func(s *settings.Settings) *bool { return &s.Timeline.ShowFilmstrip })
	w.check(timeline, "Wellenformen anzeigen", "Wellenformen in Audioclips zeichnen.",
		func(s *settings.Settings) *bool { return &s.Timeline.ShowWaveforms })
	w.check(timeline, "Ripple verschiebt alle Spuren", "Ripple-Schnitte verschieben alle nicht gesperrten Spuren mit, damit sie synchron bleiben.",
		func(s *settings.Settings) *bool { return &s.Timeline.RippleSyncLock })

	// Medien & Cache
	w.intSlider(media, "Bildspeicher Video", "Arbeitsspeicher für dekodierte Videobilder. Mehr Speicher macht Scrubben flüssiger.",
		128, 8192, 128, mb, func(s *settings.Settings) *int { return &s.Media.FrameCacheMB })
	w.intSlider(media, "Bildspeicher Standbilder", "Arbeitsspeicher für Fotos und Grafiken in Vorschaugröße.",
		64, 4096, 64, mb, func(s *settings.Settings) *int { return &s.Media.StillCacheMB })
	w.intSlider(media, "Audiospeicher", "Arbeitsspeicher für dekodierten Ton. 1,5 GB reichen für etwa zwei Stunden.",
		256, 8192, 256, mb, func(s *settings.Settings) *int { return &s.Media.AudioCacheMB })
	w.intSlider(media, "Gleichzeitige Decoder", "So viele Videos werden gleichzeitig für die Wiedergabe dekodiert.",
		1, 12, 1, func(n int) string { return fmt.Sprint(n) }, func(s *settings.Settings) *int { return &s.Media.MaxDecoders })
	w.check(media, "Unterordner mit importieren", "Beim Import eines Ordners auch die Medien in seinen Unterordnern übernehmen.",
		func(s *settings.Settings) *bool { return &s.Media.ImportRecursive })

	// Export
	w.folder(exp, "Zielordner", "Wohin exportierte Dateien standardmäßig geschrieben werden.", "Videos-Ordner",
		"Zielordner für Exporte wählen", func(s *settings.Settings) *string { return &s.Export.Folder })
	presets := []option[string]{{"", "Automatisch"}}
	for _, p := range export.Presets(nil) {
		presets = append(presets, option[string]{p.Name, p.Name})
	}
	choice(w, exp, "Standardvorgabe", "Die Vorgabe, mit der die Export-Seite startet.", presets,
		func(s *settings.Settings) *string { return &s.Export.Preset })
	w.check(exp, "Benachrichtigen, wenn fertig", "Eine Systembenachrichtigung zeigen, sobald ein Export abgeschlossen ist.",
		func(s *settings.Settings) *bool { return &s.Export.Notify })

	// Darstellung
	w.accent(display, "Akzentfarbe", "Farbe für Auswahl, Fokus und aktive Bedienelemente.")
	w.check(display, "Kompakte Darstellung", "Weniger Abstand und etwas kleinere Schrift für mehr Platz.",
		func(s *settings.Settings) *bool { return &s.Display.Compact })
	w.check(display, "Seitenwechsler anzeigen", "Die Leiste am unteren Rand mit Medien, Schnitt, Farbe, Ton und Export.",
		func(s *settings.Settings) *bool { return &s.Display.PageBar })
	w.check(display, "Statusleiste anzeigen", "Die Leiste am unteren Fensterrand.",
		func(s *settings.Settings) *bool { return &s.Display.StatusBar })
	w.check(display, "Eigene Titelleiste", "Ersetzt die Titelleiste des Systems durch die von Frame Studio. Aus: Fenster zeigen die Titelleiste des Systems.",
		func(s *settings.Settings) *bool { return &s.Display.CustomChrome })
	buttons := choice(w, display, "Fensterknöpfe", buttonsHelp(settings.Defaults().Display.WindowButtons), []option[string]{
		{"auto", "Automatisch (passend zum System)"}, {"mac", "macOS"}, {"win", "Windows"}, {"linux", "Linux"},
	}, func(s *settings.Settings) *string { return &s.Display.WindowButtons })
	loadButtons := buttons.load
	buttons.load = func(s settings.Settings) {
		loadButtons(s)
		buttons.setHelp(buttonsHelp(s.Display.WindowButtons))
	}
	display.extra = w.windowInfo
	display.onShow = func() {
		if w.infoRefresh != nil {
			w.infoRefresh()
		}
	}
	// Display.Scale: 0 follows the system, > 0 is the scale asked for (X11 / XWayland) or an
	// extra factor on the system scale (Wayland, Windows, macOS). Fyne rounds to tenths.
	scaleHelp := osscale.SettingHelp(osscale.Current().X11Path)
	choice(w, display, "Skalierung", scaleHelp, []option[float64]{
		{0, "Automatisch (wie das System)"}, {1, "100 %"}, {1.25, "125 %"}, {1.5, "150 %"}, {1.75, "175 %"}, {2, "200 %"},
	}, func(s *settings.Settings) *float64 { return &s.Display.Scale })
	w.number(display, "Eigene Skalierung", "Freier Wert von 50 bis 400 Prozent. 0 bedeutet automatisch.", "%", 0, 400, 0,
		func(s settings.Settings) float64 { return math.Round(s.Display.Scale * 100) },
		func(s *settings.Settings, v float64) { s.Display.Scale = v / 100 },
		func(s *settings.Settings, d settings.Settings) { s.Display.Scale = d.Display.Scale },
		numberOpts{allowed: func(v float64) bool { return v == 0 || v >= 50 }, rangeMsg: "Bitte 0 (automatisch) oder 50 bis 400 eingeben.", lazy: true})

	// Monitore & Fenster
	choice(w, monitors, "Fenstersystem", "Unter Wayland lassen sich Fenster nur im Vollbild einem Monitor zuordnen. Mit X11 (XWayland) lassen sie sich frei platzieren.", []option[string]{
		{"auto", "Automatisch"}, {"wayland", "Wayland"}, {"x11", "X11 (XWayland)"},
	}, func(s *settings.Settings) *string { return &s.Display.Platform })
	w.check(monitors, "Layout beim Beenden speichern", "Fenster, Monitore und Bereiche beim nächsten Start so wiederherstellen.",
		func(s *settings.Settings) *bool { return &s.Display.SaveLayoutOnQuit })
	startLayout := w.layoutChoice(monitors)
	restartFields(w, "Fenstersystem", "Skalierung", "Eigene Skalierung")
	ov := newMonitorOverview(w)
	monitors.extra = ov.view
	monitors.onShow = func() {
		startLayout.load(settings.Get()) // layouts saved, renamed or deleted meanwhile
		ov.refresh()
	}

	// Tastenkürzel
	keymap.full = func() fyne.CanvasObject {
		if w.reg == nil {
			return widget.NewLabel("Tastenkürzel sind verfügbar, sobald ein Projektfenster offen ist.")
		}
		return keymapui.Editor(w.win, w.reg)
	}
	keymap.resetExtra = func() {
		_ = settings.Update(func(s *settings.Settings) { s.Keymap = map[string]string{} })
		if w.reg != nil {
			w.reg.SetKeymap(map[string]string{})
		}
	}

	return []*category{general, playback, perf, audioCat, timeline, media, exp, display, monitors, keymap}
}

// layoutChoice is "Layout beim Start": its options are the saved layouts at the time
// it is shown or the settings change (layouts are saved, renamed and deleted while the
// window is open), plus the current value if its file is missing.
func (w *window) layoutChoice(c *category) *field {
	sel := widget.NewSelect(nil, nil)
	sel.PlaceHolder = "Standard"
	f := &field{label: "Layout beim Start", help: "Welches gespeicherte Layout beim Start gilt, wenn das letzte nicht gespeichert wurde.",
		control: sel, input: sel, reset: resetter(func(s *settings.Settings) *string { return &s.Display.Layout })}
	f.load = func(s settings.Settings) {
		opts := []string{"Standard"}
		for _, n := range layout.List() {
			if !slices.Contains(opts, n) {
				opts = append(opts, n)
			}
		}
		cur := s.Display.Layout
		if cur != "" && !slices.Contains(opts, cur) {
			opts = append(opts, cur) // e.g. renamed by hand; still shown, not "Eigener Wert"
		}
		sel.OnChanged = nil
		sel.Options = opts
		if cur == "" {
			cur = "Standard"
		}
		sel.SetSelected(cur)
		sel.OnChanged = func(v string) { w.set(func(s *settings.Settings) { s.Display.Layout = v }) }
	}
	return w.add(c, f)
}

// restartFields marks the fields with the given labels as restart-only and rebuilds
// their rows with the badge.
func restartFields(w *window, labels ...string) {
	for _, f := range w.fields {
		if slices.Contains(labels, f.label) {
			f.restart = true
			f.row = w.buildRow(f)
		}
	}
}

func px(n int) string { return fmt.Sprintf("%d px", n) }

// mb formats a memory size: "512 MB", "1,5 GB".
// FormatMB shows a memory size as the settings window does: "512 MB", "1,5 GB".
func FormatMB(n int) string { return mb(n) }

func mb(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d MB", n)
	}
	return strings.ReplaceAll(strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", float64(n)/1024), "0"), "."), ".", ",") + " GB"
}

// formatGain shows a percentage with its level in dB.
func formatGain(pct float64) string {
	if pct <= 0 {
		return "0 % · −∞ dB"
	}
	db := 20 * math.Log10(pct/100)
	s := fmt.Sprintf("%+.1f", db)
	if math.Abs(db) < 0.05 {
		s = "0,0"
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, ".", ","), "-", "−")
	return fmt.Sprintf("%.0f %% · %s dB", pct, s)
}

// styleNames are the names of the window button styles in the settings.
var styleNames = map[string]string{"mac": "macOS", "win": "Windows", "linux": "Linux"}

// buttonsHelp explains the window buttons setting and names the style in use.
func buttonsHelp(setting string) string {
	cur := styleNames[chrome.ResolveStyle(setting, runtime.GOOS)]
	return "Aussehen von Schließen, Minimieren und Maximieren: macOS (links, farbige Kreise), Windows oder Linux (rechts). " +
		"Automatisch wählt den Stil des Systems. Aktuell: " + cur + "."
}
