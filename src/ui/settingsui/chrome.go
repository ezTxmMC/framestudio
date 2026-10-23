package settingsui

import (
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/winchrome"
)

// install puts the window content together: the title row with the search box (and the
// title, with the system title bar), the sidebar and the content. With the custom title
// bar the title sits in the bar, which has only minimize and close.
func (w *window) install() {
	if w.chrome != nil {
		w.chrome.Close()
		w.chrome = nil
	}
	searchBox := container.New(&fixedWidth{w: searchW}, w.search)
	var left fyne.CanvasObject = layout.NewSpacer()
	if !w.custom {
		title := canvas.NewText("Einstellungen", studiotheme.Text)
		title.TextSize = 15
		title.TextStyle = fyne.TextStyle{Bold: true}
		left = container.NewCenter(title)
	}
	topBG := canvas.NewRectangle(studiotheme.Raised)
	top := container.NewStack(topBG, container.New(layout.NewCustomPaddedLayout(7, 7, 16, 10),
		container.NewBorder(nil, nil, left, searchBox)))
	topLine := canvas.NewRectangle(studiotheme.Border)
	topLine.SetMinSize(fyne.NewSize(1, 1))
	main := container.NewBorder(container.New(&vstack{}, top, topLine), nil, w.sideObj, nil, w.contentObj)
	if !w.custom {
		w.win.SetContent(main)
		return
	}
	title := canvas.NewText("Einstellungen", studiotheme.Text)
	title.TextSize = 12
	title.TextStyle = fyne.TextStyle{Bold: true}
	o := winchrome.Options(settings.Get().Display, true, false, false)
	root, c := winchrome.Build(w.win, o, winchrome.Parts{Center: container.NewCenter(title)}, main)
	w.chrome = c
	w.win.SetContent(root)
}

// decorate takes the system title bar off the native window, or gives it back. Call it
// once the window is shown. If the window system refuses, the system title bar stays.
func (w *window) decorate() {
	if w.custom {
		if err := winchrome.Undecorate(w.win); err != nil {
			w.custom = false
			w.install()
		} else {
			w.undecorated = true
		}
	} else if w.undecorated {
		_ = winchrome.Redecorate(w.win)
		w.undecorated = false
	}
}

// applyChrome follows Display.CustomChrome and Display.WindowButtons live.
func (w *window) applyChrome(old, s settings.Display) {
	if want := winchrome.Wanted(s); want != w.custom {
		w.custom = want
		w.install()
		w.decorate()
		return
	}
	if w.chrome != nil {
		w.chrome.Bar.SetStyle(winchrome.Style(s))
	}
}

// CloseFocused closes the settings window when it has the focus and reports whether it
// did (the command "Fenster schließen"). UI thread only.
func CloseFocused() bool {
	if current == nil || current.closed || !winchrome.Sys.State(current.win).Focused {
		return false
	}
	current.win.Close()
	return true
}

// pendingChromeRestart reports whether the custom title bar needs a restart to take
// effect: on a Wayland session it runs through XWayland, a window system chosen at start.
func pendingChromeRestart(s settings.Settings) bool {
	if envOverride("Fenstersystem") != "" {
		return false
	}
	st := startupSettings()
	return winchrome.WantsXWayland(s.Display.CustomChrome, s.Display.Platform, "") !=
		winchrome.WantsXWayland(st.Display.CustomChrome, st.Display.Platform, "")
}

// windowInfo is the block below the Darstellung rows: the window system, whether the
// custom title bar is active, and why not when it is inactive.
func (w *window) windowInfo() fyne.CanvasObject {
	head := canvas.NewText("FENSTER UND TITELLEISTE", studiotheme.Muted)
	head.TextSize = 11
	head.TextStyle = fyne.TextStyle{Bold: true}
	system := widget.NewLabel("")
	system.Wrapping = fyne.TextWrapWord
	state := widget.NewLabel("")
	state.Wrapping = fyne.TextWrapWord
	note := widget.NewLabel("Unter Wayland läuft die eigene Titelleiste über XWayland, ohne Schatten des Fensterverwalters. " +
		"Ausgeschaltet bleibt das native Wayland-Fenster mit der Titelleiste des Systems. Beim Umschalten ist ein Neustart nötig.")
	note.Wrapping = fyne.TextWrapWord
	w.infoRefresh = func() {
		d := settings.Get().Display
		system.SetText("Fenstersystem: " + winchrome.PlatformLabel(winchrome.CurrentPlatform(), os.Getenv))
		_, text := winchrome.Describe(d)
		state.SetText("Eigene Titelleiste: " + text)
	}
	w.infoRefresh()
	return container.New(layout.NewCustomPaddedLayout(14, 14, rowPadX, rowPadX),
		container.NewVBox(head, system, state, note))
}
