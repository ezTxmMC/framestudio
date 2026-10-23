package shell

import (
	"fyne.io/fyne/v2/container"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/chrome"
	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/settingsui"
	"framestudio-proto/src/ui/winchrome"
)

// Custom window chrome of the main window: with Display.CustomChrome and a window
// system that can move and resize borderless windows, the menu row becomes the title
// bar (window buttons, wordmark, menus, project name) inside a resize frame. Otherwise
// the system title bar stays and the menu row sits below it as before.

// installContent puts the window content together for the current mode.
func (sh *Shell) installContent() {
	if sh.chrome != nil {
		sh.chrome.Close()
		sh.chrome = nil
	}
	if sh.chromeOn {
		root, c := winchrome.Build(sh.win, winchrome.Options(settings.Get().Display, false, true, true), sh.bars.titleParts(), sh.root)
		sh.chrome = c
		sh.win.SetContent(root)
	} else {
		sh.win.SetContent(container.NewBorder(sh.bars.systemRow(), nil, nil, nil, sh.root))
	}
}

// CustomChrome reports whether the main window shows the custom title bar.
func (sh *Shell) CustomChrome() bool { return sh.chromeOn }

// decorate makes the native window match the mode: the system title bar goes when the
// custom one is shown, and returns when it is not. Call it once the window exists (Run
// does right after Show) and after every mode change. When the window system refuses,
// the system title bar stays and the content falls back; winchrome.Failure keeps the
// reason for the diagnostics.
func (sh *Shell) decorate() {
	if sh.chromeOn {
		if err := winchrome.Undecorate(sh.win); err != nil {
			sh.chromeOn = false
			sh.retryDecorate = true // the native window may not exist yet: see Run
			sh.installContent()
		} else {
			sh.undecorated = true
		}
	} else if sh.undecorated {
		_ = winchrome.Redecorate(sh.win)
		sh.undecorated = false
	}
	sh.watchWindow()
	sh.refreshChrome()
}

// decorateAgain runs once the run loop has started: if the first attempt came before
// the native window existed, the window system gets asked again.
func (sh *Shell) decorateAgain() {
	if !sh.retryDecorate {
		return
	}
	sh.retryDecorate = false
	if winchrome.Wanted(settings.Get().Display) {
		sh.chromeOn = true
		sh.installContent()
		sh.decorate()
	}
}

// watchWindow follows focus, maximize and fullscreen changes of the window so menus and
// check marks (Vollbild) stay in sync with the title bar buttons.
func (sh *Shell) watchWindow() {
	if sh.unwatch != nil {
		return
	}
	sh.unwatch = winchrome.Sys.Watch(sh.win, func(chrome.State) { sh.reg.Refresh() })
}

// releaseChrome stops the window watch and the title bar (dispose).
func (sh *Shell) releaseChrome() {
	if sh.unwatch != nil {
		sh.unwatch()
		sh.unwatch = nil
	}
	if sh.chrome != nil {
		sh.chrome.Close()
		sh.chrome = nil
	}
	winchrome.Forget(sh.win)
}

// applyChromeSettings reacts to a changed Display.CustomChrome or Display.WindowButtons:
// the top row is rebuilt (projects, pages and menus stay) and the window re-decorated.
func (sh *Shell) applyChromeSettings(old, s settings.Display) {
	want := winchrome.Wanted(s)
	if want != sh.chromeOn {
		sh.chromeOn = want
		sh.installContent()
		sh.decorate()
		return
	}
	if sh.chrome != nil && s.WindowButtons != old.WindowButtons {
		sh.chrome.Bar.SetStyle(winchrome.Style(s))
	}
}

// setFullscreen puts the main window fullscreen on the monitor it is on, or back.
func (sh *Shell) setFullscreen(on bool) {
	if err := display.Fullscreen(sh.win, "", on); err != nil || sh.win.FullScreen() != on {
		sh.win.SetFullScreen(on)
	}
}

// closeFocusedWindow is window.close: it closes the window the command was given in, a
// detached or settings window when one has the focus, else the main window (quit).
func (sh *Shell) closeFocusedWindow() {
	if sh.ws.CloseFocused() || settingsui.CloseFocused() {
		return
	}
	sh.Quit()
}
