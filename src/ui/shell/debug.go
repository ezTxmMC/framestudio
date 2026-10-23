package shell

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/ui/display"
)

// Multi-monitor self check (FS_DEBUG_MULTIMON). For checking the window placement on a
// real desk without clicking: once the app has started it detaches the panels listed in
// the variable, waits, prints the monitors and the geometry of every window to stdout
// and quits. Format: "panel=monitor:mode,..." with mode fullscreen, maximized or window;
// "1" means "cleanfeed=HDMI-A-1:fullscreen,timeline=DP-3:window".

type debugPlacement struct{ panel, monitor, mode string }

func parseDebugMultimon(v string) []debugPlacement {
	if v == "1" || strings.EqualFold(v, "true") {
		v = "cleanfeed=HDMI-A-1:fullscreen,timeline=DP-3:window"
	}
	var out []debugPlacement
	for _, part := range strings.Split(v, ",") {
		panel, rest, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || panel == "" {
			continue
		}
		mon, mode, _ := strings.Cut(rest, ":")
		if mode == "" {
			mode = "window"
		}
		out = append(out, debugPlacement{panel, mon, mode})
	}
	return out
}

// runMultimonCheck runs the self check on its own goroutine; UI work hops to the UI
// thread. hold is how long the windows stay up before the report.
func (sh *Shell) runMultimonCheck(spec string, hold time.Duration, out io.Writer) {
	places := parseDebugMultimon(spec)
	go func() {
		time.Sleep(800 * time.Millisecond) // let the main window map first
		fmt.Fprintf(out, "platform=%s canPosition=%v\n", display.Platform(), display.CanPosition())
		for _, m := range display.Monitors() {
			fmt.Fprintf(out, "monitor %-9s x=%d y=%d %dx%d %dHz scale=%.2f primary=%v\n",
				m.ID, m.X, m.Y, m.W, m.H, m.Refresh, m.Scale, m.Primary)
		}
		fyne.DoAndWait(func() {
			for _, p := range places {
				switch p.mode {
				case "fullscreen":
					sh.ws.Detach(p.panel, p.monitor, true)
				default:
					sh.ws.Detach(p.panel, p.monitor, false)
					if p.mode == "maximized" {
						if w := sh.ws.Window(p.panel); w != nil {
							if err := display.Maximize(w, p.monitor); err != nil {
								fmt.Fprintf(out, "maximize %s: %v\n", p.panel, err)
							}
						}
					}
				}
			}
		})
		time.Sleep(hold)
		report := func(name string, w fyne.Window) {
			if w == nil {
				fmt.Fprintf(out, "window %-10s not open\n", name)
				return
			}
			id, x, y, ww, hh, ok := display.Geometry(w)
			var full bool
			fyne.DoAndWait(func() { full = w.FullScreen() })
			fmt.Fprintf(out, "window %-10s monitor=%s x=%d y=%d %dx%d known=%v fullscreen=%v maximized=%v\n",
				name, id, x, y, ww, hh, ok, full, display.IsMaximized(w))
		}
		report("main", sh.win)
		for _, p := range places {
			var w fyne.Window
			fyne.DoAndWait(func() { w = sh.ws.Window(p.panel) })
			report(p.panel+"→"+p.monitor, w)
		}
		fyne.Do(sh.Quit)
	}()
}

// startDebugHooks starts the self checks requested through the environment.
func (sh *Shell) startDebugHooks() {
	if v := os.Getenv("FS_DEBUG_MULTIMON"); v != "" {
		sh.runMultimonCheck(v, 3*time.Second, os.Stdout)
	}
}
