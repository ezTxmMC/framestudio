package application

import (
	"fmt"
	"framestudio-proto/src/ui/viewer"
	"log"
	"os"
	"sync/atomic"
	"syscall"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"framestudio-proto/src/media"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/liveaudio"
	"framestudio-proto/src/ui/osscale"
	"framestudio-proto/src/ui/settingsui"
	"framestudio-proto/src/ui/shell"
	studiotheme "framestudio-proto/src/ui/theme"
)

// Run starts Frame Studio, opening any project files given on the command line.
//
// Before the Fyne app exists it loads the settings and applies the start-up ones
// (window system, scale) through FYNE_PLATFORM / FYNE_SCALE unless the user set those.
// A settings file that cannot be read is reported once the window is up (see
// settingsui.TakeStartupError). After the app quits, a restart requested from the
// settings window (settingsui.RequestRestart) starts the program again.
func Run(files []string) {
	s, loadErr := settings.Load()
	startMemory()
	// Read before applyEnv sets the variables: only what the user set counts as theirs
	// (a restart passes the environment on without the derived ones, see RestartEnv).
	settingsui.SetEnvOverrides(EnvOverrides(os.Getenv))
	settingsui.SetStartupWarning(StartWarning(s, os.Getenv))
	env, rep := planStart(s, os.Getenv)
	applyEnv(env)
	osscale.SetReport(rep)
	log.Printf("Skalierung: %s, System %s (%s), Einstellung %v, FYNE_SCALE=%q", rep.GOOS,
		osscale.Percent(rep.OS.Scale), rep.OS.Source.Label(), rep.Setting, os.Getenv(envScale))
	settingsui.SetStartup(s)
	if loadErr != nil {
		settingsui.SetStartupError(loadErr)
	}
	studiotheme.Configure(s.Display.Accent, s.Display.Compact)

	a := app.NewWithID("de.framestudio.proto")
	a.Settings().SetTheme(studiotheme.New())
	stop := Follow()
	a.Lifecycle().SetOnStarted(func() { showStartupError(a) })
	shell.Run(a, files)
	stop()
	if settingsui.RestartRequested() {
		restart()
	}
}

// Follow applies the settings to the running subsystems now and after every change:
// accent colour and density (theme refresh), media cache limits and live audio. Call the
// returned stop when the app ends. UI thread only.
func Follow() (stop func()) {
	apply := func(s settings.Settings) {
		if studiotheme.Configure(s.Display.Accent, s.Display.Compact) {
			studiotheme.Refresh()
		}
		mediaCaches.Set(MediaConfig(s))
	}
	apply(settings.Get())
	unsub := settings.Subscribe(apply)
	stopAudio := liveaudio.FollowSettings()
	return func() {
		unsub()
		stopAudio()
	}
}

var (
	mediaCaches = newMediaBudget()
	memWatcher  atomic.Pointer[MemoryWatcher]
)

// startMemory sets the GC limits (R9) and starts the memory watcher that shrinks the
// caches when the heap nears the limit. Environment variables GOGC and GOMEMLIMIT of
// the user win.
func startMemory() {
	total := SystemRAM(os.ReadFile)
	limit := EffectiveLimit(TuneGC(os.Getenv, total), total) // also with the user's GOMEMLIMIT
	if limit <= 0 {
		return
	}
	w := NewMemoryWatcher(limit, nil)
	w.Add(mediaCaches)
	w.Add(viewer.Budgets{})
	memWatcher.Store(w)
	w.Start()
}

// MemoryBudgets lets other caches (the preview frame cache) take part in the memory
// watcher: they are scaled down under pressure. Safe before and after startMemory.
func MemoryBudgets(b Budgeter) {
	if w := memWatcher.Load(); w != nil {
		w.Add(b)
	}
}

// MediaConfig maps the settings onto the media cache limits.
func MediaConfig(s settings.Settings) media.Config {
	frames := s.Media.FrameCacheMB
	if mb := s.Playback.CacheMB; mb > 0 {
		frames = max(frames, mb/3) // the third of the preview cache not kept as rendered frames
	}
	return media.Config{FrameCacheMB: frames, StillCacheMB: s.Media.StillCacheMB, MaxDecoders: s.Media.MaxDecoders}
}

// showStartupError shows a problem from before the UI existed, unless the shell took
// it already.
func showStartupError(a fyne.App) {
	err := settingsui.TakeStartupError()
	if err == nil {
		return
	}
	if ws := a.Driver().AllWindows(); len(ws) > 0 {
		dialog.ShowError(err, ws[0])
	}
}

// restart replaces the process with a fresh copy of the program with the same
// arguments. The environment variables derived from the old settings are dropped so the
// new settings decide.
func restart() {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	if err := syscall.Exec(exe, os.Args, RestartEnv(os.Environ(), applied)); err != nil {
		fmt.Fprintln(os.Stderr, "Frame Studio konnte nicht neu gestartet werden:", err)
	}
}
