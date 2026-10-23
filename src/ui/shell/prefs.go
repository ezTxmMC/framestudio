package shell

import (
	"errors"
	"fmt"
	"framestudio-proto/src/ui/settingsui"
	"maps"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/store"
)

// settingsChanged applies changed settings live (UI thread; settings.Update runs there).
func (sh *Shell) settingsChanged(s settings.Settings) {
	old := sh.lastSettings
	sh.lastSettings = s
	if !maps.Equal(s.Keymap, sh.reg.Keymap()) {
		sh.reg.SetKeymap(s.Keymap) // re-registers the window shortcuts
	}
	if s.General.ProjectsDir != old.General.ProjectsDir {
		store.SetProjectsDir(s.General.ProjectsDir)
		if sh.showManager {
			sh.mgr.reload()
		}
	}
	if s.General.AutosaveSeconds != old.General.AutosaveSeconds {
		select { // keep only the newest interval for the autosave loop
		case <-sh.autosaveSet:
		default:
		}
		sh.autosaveSet <- autosaveInterval(s)
	}
	if s.Display.PageBar != old.Display.PageBar || s.Display.StatusBar != old.Display.StatusBar {
		sh.bars.applyDisplay(s.Display)
	}
	sh.reg.Refresh()
}

// autosaveInterval is the autosave period of s; 0 turns autosave off.
func autosaveInterval(s settings.Settings) time.Duration {
	if s.General.AutosaveSeconds <= 0 {
		return 0
	}
	return time.Duration(max(s.General.AutosaveSeconds, 10)) * time.Second
}

// saveLastOpen remembers the project files open now, in tab order, for "Zuletzt
// geöffnete Projekte wieder öffnen" at the next start.
func (sh *Shell) saveLastOpen() {
	var paths []string
	for _, p := range sh.projects {
		if p.sess.Path != "" {
			paths = append(paths, p.sess.Path)
		}
	}
	if err := settings.Update(func(s *settings.Settings) { s.General.LastOpen = paths }); err != nil {
		fyne.LogError("Geöffnete Projekte merken", err)
	}
}

var templateIDPattern = regexp.MustCompile(`^(\d+)p(\d+)$`)

// templateByID finds the new-project template of a settings ID: "1080p25" (landscape
// height and frame rate) or "1080x1920@30" (width × height @ fps). Unknown IDs give the
// first template.
func templateByID(id string) Template {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, st := range settingsui.Templates { // the IDs the settings window stores
		if strings.EqualFold(st.ID, id) {
			for _, t := range Templates {
				if t.Width == st.Width && t.Height == st.Height && t.FPS == st.FPS {
					return t
				}
			}
			return Template{Label: st.Label, Width: st.Width, Height: st.Height, FPS: st.FPS}
		}
	}
	for _, t := range Templates {
		if id == templateID(t) {
			return t
		}
	}
	if m := templateIDPattern.FindStringSubmatch(id); m != nil {
		h, _ := strconv.Atoi(m[1])
		fps, _ := strconv.Atoi(m[2])
		for _, t := range Templates {
			if t.Height == h && t.FPS == fps && t.Width > t.Height {
				return t
			}
		}
	}
	return Templates[0]
}

// templateID is the settings ID of a template: "1080p25" for landscape, else "WxH@fps".
func templateID(t Template) string {
	if t.Width > t.Height {
		return fmt.Sprintf("%dp%d", t.Height, t.FPS)
	}
	return fmt.Sprintf("%dx%d@%d", t.Width, t.Height, t.FPS)
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func errSettingsLoad(err error) error {
	return fmt.Errorf("Die Einstellungen konnten nicht geladen werden. Frame Studio arbeitet mit den Standardwerten weiter. %v", err)
}

func errSettingsSave(err error) error {
	return fmt.Errorf("Die Einstellung gilt, konnte aber nicht gespeichert werden. Rechte im Einstellungsordner prüfen (%v)", err)
}

func errConfigDir(err error) error {
	return fmt.Errorf("Der Einstellungsordner ist nicht verfügbar: %v", err)
}

func errOpenFolder(dir string, err error) error {
	return fmt.Errorf("Der Ordner %s lässt sich nicht öffnen. Ist xdg-open installiert? (%v)", dir, err)
}

func errMissingLast(names []string) error {
	if len(names) == 1 {
		return errors.New("„" + names[0] + "“ aus der letzten Sitzung gibt es nicht mehr. Wurde die Datei verschoben oder gelöscht?")
	}
	return fmt.Errorf("%d Projekte aus der letzten Sitzung gibt es nicht mehr: %s. Wurden sie verschoben oder gelöscht?",
		len(names), strings.Join(names, ", "))
}
