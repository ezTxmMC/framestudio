// Package layout is the saved arrangement of the workspace: split positions and hidden
// areas per page, the main window and every detached panel window with its monitor and
// mode. Layouts are JSON files in $XDG_CONFIG_HOME/framestudio/layouts/<name>.json;
// "_current.json" holds the state at the last quit.
package layout

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"framestudio-proto/src/settings"
)

const Version = 1

// Window modes.
const (
	ModeWindow     = "window"
	ModeMaximized  = "maximized"
	ModeFullscreen = "fullscreen"
)

type Rect struct{ X, Y, W, H int }

// Window is the main window (Panel == "") or a detached panel window.
type Window struct {
	ID      string `json:"id"`
	Panel   string `json:"panel,omitempty"`   // page.Panel* ID; "" = main window
	Monitor string `json:"monitor,omitempty"` // monitor name like "DP-2"; "" = let the system decide
	Mode    string `json:"mode"`
	Rect    Rect   `json:"rect"` // windowed geometry relative to the monitor's top-left
}

// Page is the layout of one workspace page.
type Page struct {
	Splits map[string]float64 `json:"splits,omitempty"` // split ID -> offset 0..1
	Hidden map[string]bool    `json:"hidden,omitempty"` // area ID -> hidden
	Extra  map[string]string  `json:"extra,omitempty"`  // page-specific (e.g. selected scope)
}

type Layout struct {
	Version    int             `json:"version"`
	Name       string          `json:"name"`
	ActivePage string          `json:"activePage,omitempty"`
	Windows    []Window        `json:"windows"`
	Pages      map[string]Page `json:"pages"`
}

// Main returns the main window entry, adding a default one if missing.
func (l *Layout) Main() *Window {
	for i := range l.Windows {
		if l.Windows[i].Panel == "" {
			return &l.Windows[i]
		}
	}
	l.Windows = append([]Window{{ID: "main", Mode: ModeMaximized, Rect: Rect{W: 1440, H: 900}}}, l.Windows...)
	return &l.Windows[0]
}

// Default is the single-window layout.
func Default() Layout {
	l := Layout{Version: Version, Name: "Standard", ActivePage: "edit", Pages: map[string]Page{}}
	l.Main()
	return l
}

func dir() (string, error) {
	d, err := settings.Dir()
	if err != nil {
		return "", err
	}
	d = filepath.Join(d, "layouts")
	return d, os.MkdirAll(d, 0o755)
}

func fileName(name string) string {
	clean := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if clean == "" {
		clean = "Layout"
	}
	return clean + ".json"
}

// Save writes l under its name (or "_current" when current is true).
func Save(l Layout, current bool) error {
	d, err := dir()
	if err != nil {
		return err
	}
	l.Version = Version
	name := fileName(l.Name)
	if current {
		name = "_current.json"
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(d, name)
	if err := os.WriteFile(p+".tmp", b, 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// Load reads a saved layout by name, or the last-quit state when current is true.
func Load(name string, current bool) (Layout, error) {
	d, err := dir()
	if err != nil {
		return Default(), err
	}
	p := filepath.Join(d, fileName(name))
	if current {
		p = filepath.Join(d, "_current.json")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Default(), err
	}
	var l Layout
	if err := json.Unmarshal(b, &l); err != nil {
		return Default(), errors.New("Layout ist beschädigt: " + err.Error())
	}
	if l.Pages == nil {
		l.Pages = map[string]Page{}
	}
	l.Main()
	return l, nil
}

// List returns the names of saved layouts, sorted.
func List() []string {
	d, err := dir()
	if err != nil {
		return nil
	}
	entries, _ := os.ReadDir(d)
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".json") && !strings.HasPrefix(n, "_") {
			if l, err := Load(strings.TrimSuffix(n, ".json"), false); err == nil {
				names = append(names, l.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// Delete removes a saved layout.
func Delete(name string) error {
	d, err := dir()
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(d, fileName(name)))
}
