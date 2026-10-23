// Package settings holds the user preferences of Frame Studio, persisted as JSON in
// $XDG_CONFIG_HOME/framestudio/settings.json. Get/Update are UI-thread only; subsystems
// that run elsewhere receive copies through Subscribe.
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

const Version = 1

type Settings struct {
	Version int `json:"version"`

	General  General           `json:"general"`
	Playback Playback          `json:"playback"`
	Audio    Audio             `json:"audio"`
	Timeline Timeline          `json:"timeline"`
	Media    Media             `json:"media"`
	Export   Export            `json:"export"`
	Display  Display           `json:"display"`
	Keymap   map[string]string `json:"keymap"` // command ID -> shortcut overrides
}

type General struct {
	Startup         string   `json:"startup"` // "manager" | "last" (reopen last open projects) | "new"
	AutosaveSeconds int      `json:"autosaveSeconds"`
	ProjectsDir     string   `json:"projectsDir"` // empty = default library
	DefaultTemplate string   `json:"defaultTemplate"`
	ConfirmClose    bool     `json:"confirmClose"`
	LastOpen        []string `json:"lastOpen"` // project paths open at last quit
}

type Playback struct {
	PreviewQuality string `json:"previewQuality"` // "auto" | "full" | "half" | "quarter"
	MaxPreviewH    int    `json:"maxPreviewHeight"`
	Follow         string `json:"follow"` // "page" | "smooth" | "off"
	LoopDefault    bool   `json:"loopDefault"`
	SafeArea       bool   `json:"safeArea"`
}

type Audio struct {
	Player     string  `json:"player"`    // "auto" | "pw-play" | "paplay" | "ffplay"
	LatencyMS  int     `json:"latencyMs"` // 0 = automatic estimate
	ScrubAudio bool    `json:"scrubAudio"`
	Muted      bool    `json:"muted"`
	MasterGain float64 `json:"masterGain"` // linear monitor gain, 1 = unity (preview only)
}

type Timeline struct {
	Snapping       bool    `json:"snapping"`
	StillSeconds   float64 `json:"stillSeconds"`
	GeneratorSecs  float64 `json:"generatorSeconds"`
	FadeSeconds    float64 `json:"fadeSeconds"`
	VideoTrackH    int     `json:"videoTrackHeight"`
	AudioTrackH    int     `json:"audioTrackHeight"`
	ShowFilmstrip  bool    `json:"showFilmstrip"`
	ShowWaveforms  bool    `json:"showWaveforms"`
	RippleSyncLock bool    `json:"rippleSyncLock"` // ripple edits shift all unlocked tracks
}

type Media struct {
	FrameCacheMB    int  `json:"frameCacheMb"`
	StillCacheMB    int  `json:"stillCacheMb"`
	AudioCacheMB    int  `json:"audioCacheMb"`
	ImportRecursive bool `json:"importRecursive"`
	MaxDecoders     int  `json:"maxDecoders"`
}

type Export struct {
	Folder string `json:"folder"` // empty = ~/Videos or home
	Preset string `json:"preset"`
	Notify bool   `json:"notify"`
}

type Display struct {
	Platform         string  `json:"platform"` // "auto" | "wayland" | "x11"  (applied at next start)
	Scale            float64 `json:"scale"`    // 0 = system default (applied at next start)
	Accent           string  `json:"accent"`   // "steel" | "teal" | "amber" | "violet"
	Compact          bool    `json:"compact"`
	PageBar          bool    `json:"pageBar"`
	StatusBar        bool    `json:"statusBar"`
	SaveLayoutOnQuit bool    `json:"saveLayoutOnQuit"`
	Layout           string  `json:"layout"` // name of the active layout
}

// Defaults returns the factory settings.
func Defaults() Settings {
	return Settings{
		Version:  Version,
		General:  General{Startup: "manager", AutosaveSeconds: 90, DefaultTemplate: "1080p25", ConfirmClose: true},
		Playback: Playback{PreviewQuality: "auto", MaxPreviewH: 720, Follow: "page"},
		Audio:    Audio{Player: "auto", ScrubAudio: true, MasterGain: 1},
		Timeline: Timeline{Snapping: true, StillSeconds: 5, GeneratorSecs: 5, FadeSeconds: 1, VideoTrackH: 58, AudioTrackH: 48,
			ShowFilmstrip: true, ShowWaveforms: true, RippleSyncLock: true},
		Media:   Media{FrameCacheMB: 512, StillCacheMB: 256, AudioCacheMB: 1536, MaxDecoders: 4},
		Export:  Export{Notify: true},
		Display: Display{Platform: "auto", Accent: "steel", PageBar: true, StatusBar: true, SaveLayoutOnQuit: true, Layout: "Standard"},
		Keymap:  map[string]string{},
	}
}

var (
	mu        sync.Mutex
	current   = Defaults()
	listeners = map[int]func(Settings){}
	nextID    int
	path      string
)

// Dir is the config folder ($XDG_CONFIG_HOME/framestudio), created on demand.
func Dir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "framestudio")
	return dir, os.MkdirAll(dir, 0o755)
}

// Load reads settings.json (missing file = defaults; unknown fields ignored; missing
// fields keep their defaults) and makes it current.
func Load() (Settings, error) {
	dir, err := Dir()
	if err != nil {
		return Get(), err
	}
	p := filepath.Join(dir, "settings.json")
	s := Defaults()
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Get(), err
	}
	if err == nil {
		if err := json.Unmarshal(b, &s); err != nil {
			return Get(), errors.New("Einstellungen sind beschädigt und wurden auf Standard zurückgesetzt: " + err.Error())
		}
	}
	if s.Keymap == nil {
		s.Keymap = map[string]string{}
	}
	s.Version = Version
	mu.Lock()
	path = p
	current = s
	mu.Unlock()
	return s, nil
}

// Get returns a copy of the current settings.
func Get() Settings {
	mu.Lock()
	defer mu.Unlock()
	return clone(current)
}

// Update changes the settings, saves them and notifies subscribers.
func Update(fn func(s *Settings)) error {
	mu.Lock()
	next := clone(current)
	fn(&next)
	current = next
	p := path
	ls := make([]func(Settings), 0, len(listeners))
	for _, l := range listeners {
		ls = append(ls, l)
	}
	mu.Unlock()
	for _, l := range ls {
		l(clone(next))
	}
	if p == "" {
		return nil // Load was never called (tests): keep in memory only
	}
	return save(p, next)
}

// Reset restores the factory settings (keeps LastOpen).
func Reset() error {
	return Update(func(s *Settings) {
		last := s.General.LastOpen
		*s = Defaults()
		s.General.LastOpen = last
	})
}

// Subscribe calls fn with the new settings after every Update.
func Subscribe(fn func(Settings)) (unsubscribe func()) {
	mu.Lock()
	defer mu.Unlock()
	id := nextID
	nextID++
	listeners[id] = fn
	return func() {
		mu.Lock()
		delete(listeners, id)
		mu.Unlock()
	}
}

func save(p string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func clone(s Settings) Settings {
	c := s
	c.Keymap = map[string]string{}
	for k, v := range s.Keymap {
		c.Keymap[k] = v
	}
	c.General.LastOpen = append([]string(nil), s.General.LastOpen...)
	return c
}
