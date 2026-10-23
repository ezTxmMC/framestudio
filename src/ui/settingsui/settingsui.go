// Package settingsui is the preferences window: categories on the left (Allgemein,
// Wiedergabe, Audio, Timeline, Medien & Cache, Export, Darstellung, Monitore & Fenster,
// Tastenkürzel), live-applied controls on the right.
//
// OWNER: settings agent. Exported signatures in this file are a contract.
package settingsui

import (
	"strings"
	"sync"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/model"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
)

// Restart closes the app cleanly and starts it again. The shell sets it: it runs the
// normal quit flow (save prompts, layout, audio) and, once quitting is certain, calls
// RequestRestart before the app's run loop ends; application.Run then starts the program
// again with the same arguments. While it is nil the window only tells the user to
// restart by hand. UI thread only.
var Restart func()

var (
	stateMu    sync.Mutex
	restarting bool
	startup    *settings.Settings // settings in effect since start (restart fields)
	startupErr error
	startWarn  string
	envOver    map[string]string // setting label → "FYNE_SCALE=1.5"
)

// SetStartupWarning keeps a start-up notice (the chosen window system was not
// available) until the shell shows it.
func SetStartupWarning(msg string) {
	stateMu.Lock()
	startWarn = msg
	stateMu.Unlock()
}

// TakeStartupWarning returns the notice of SetStartupWarning once ("" after).
func TakeStartupWarning() string {
	stateMu.Lock()
	defer stateMu.Unlock()
	msg := startWarn
	startWarn = ""
	return msg
}

// SetEnvOverrides records which restart-only settings the user's environment decides
// (label "Fenstersystem" or "Skalierung" → "FYNE_SCALE=1.5"). Their rows say so, and
// changing them shows no restart banner, since a restart would not apply them.
func SetEnvOverrides(m map[string]string) {
	stateMu.Lock()
	envOver = m
	stateMu.Unlock()
}

func envOverride(label string) string {
	stateMu.Lock()
	defer stateMu.Unlock()
	if label == "Eigene Skalierung" {
		label = "Skalierung"
	}
	return envOver[label]
}

// RequestRestart marks that the program should start again after it quits.
func RequestRestart() {
	stateMu.Lock()
	restarting = true
	stateMu.Unlock()
}

// CancelRestart withdraws RequestRestart (e.g. the user cancelled quitting).
func CancelRestart() {
	stateMu.Lock()
	restarting = false
	stateMu.Unlock()
}

// RestartRequested reports whether RequestRestart was called (and not cancelled).
func RestartRequested() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	return restarting
}

// SetStartup records the settings the program started with, so the window can tell
// which restart-only changes (platform, scale) are still pending. application.Run calls
// it after settings.Load; without it the first Show records the then current settings.
func SetStartup(s settings.Settings) {
	stateMu.Lock()
	startup = &s
	stateMu.Unlock()
}

func startupSettings() settings.Settings {
	stateMu.Lock()
	defer stateMu.Unlock()
	if startup == nil {
		s := settings.Get()
		startup = &s
	}
	return *startup
}

// SetStartupError keeps a problem found before the UI existed (settings file could not
// be read) until the shell shows it.
func SetStartupError(err error) {
	stateMu.Lock()
	startupErr = err
	stateMu.Unlock()
}

// TakeStartupError returns the problem recorded with SetStartupError once (nil after).
// The shell calls it when its window is up and shows the error.
func TakeStartupError() error {
	stateMu.Lock()
	defer stateMu.Unlock()
	err := startupErr
	startupErr = nil
	return err
}

// current is the open settings window (only one at a time). UI thread only.
var current *window

// Show opens the settings window (category optional: "audio", "keymap", ... or the
// German title). If it is open already it comes to the front on that category.
func Show(a fyne.App, parent fyne.Window, reg *command.Registry, category string) {
	if a == nil {
		a = fyne.CurrentApp()
	}
	if current != nil && !current.closed {
		current.showCategory(category)
		current.win.Show()
		current.win.RequestFocus()
		return
	}
	w := newWindow(a, parent, reg)
	current = w
	w.showCategory(category)
	w.win.Show()
	w.decorate()
}

// Close closes the settings window if it is open. UI thread only.
func Close() {
	if current != nil {
		current.win.Close()
	}
}

// Categories lists the category IDs in display order.
func Categories() []string {
	return []string{catGeneral, catPlayback, catAudio, catTimeline, catMedia, catExport, catDisplay, catMonitors, catKeymap}
}

// Template is a project preset offered for new projects (settings.General.DefaultTemplate).
type Template struct {
	ID            string
	Label         string
	Width, Height int
	FPS           model.Rate
}

// Templates are the choices for "Vorlage für neue Projekte". The IDs are what
// settings.General.DefaultTemplate stores; the shell maps them to its project templates.
var Templates = []Template{
	{"1080p25", "1080p HD · 25 fps", 1920, 1080, model.IntRate(25)},
	{"720p25", "720p HD · 25 fps", 1280, 720, model.IntRate(25)},
	{"2160p25", "4K UHD · 25 fps", 3840, 2160, model.IntRate(25)},
	{"1080p30", "1080p HD · 30 fps", 1920, 1080, model.IntRate(30)},
	{"1080p60", "1080p HD · 60 fps", 1920, 1080, model.IntRate(60)},
	{"vertical30", "Hochkant 1080×1920 · 30 fps", 1080, 1920, model.IntRate(30)},
	{"square30", "Quadrat 1080×1080 · 30 fps", 1080, 1080, model.IntRate(30)},
}

// TemplateByID returns the template with id (the first one for unknown IDs).
func TemplateByID(id string) Template {
	for _, t := range Templates {
		if strings.EqualFold(t.ID, id) {
			return t
		}
	}
	return Templates[0]
}
