// Package store saves and loads Frame Studio projects (*.fsproj, JSON) and keeps the
// list of recent projects.
//
// OWNER: shell agent. Exported signatures in this file are a contract; do not change them.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"framestudio-proto/src/model"
)

const Extension = ".fsproj"

// Entry describes a project file for the project manager.
type Entry struct {
	Path     string
	Name     string
	Modified time.Time
	Duration int // frames
	FPS      int
}

const maxRecent = 15

// recentMu serialises access to recent.json.
var recentMu sync.Mutex

// ProjectsDir is the default library folder ($XDG_DATA_HOME/framestudio/projects), created on demand.
func ProjectsDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("Projektordner nicht gefunden: kein Benutzerverzeichnis (%v)", err)
		}
		base = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(base, "framestudio", "projects")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("Projektordner %s kann nicht angelegt werden: %w", dir, err)
	}
	return dir, nil
}

// Save writes p atomically (temp file + rename) to path.
func Save(p *model.Project, path string) error {
	if p == nil {
		return errors.New("Kein Projekt zum Speichern vorhanden")
	}
	if p.Version == 0 {
		p.Version = model.FormatVersion
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("Projekt kann nicht gespeichert werden: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("Ordner %s kann nicht angelegt werden. Rechte prüfen oder anderen Ort wählen (%w)", dir, err)
	}
	return writeAtomic(path, data)
}

// writeAtomic writes data to a temp file next to path, syncs it and renames it over path.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("In %s kann nicht geschrieben werden. Rechte prüfen oder anderen Ort wählen (%w)", dir, err)
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("Speichern nach %s fehlgeschlagen. Freien Speicherplatz und Rechte prüfen (%w)", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Rename(name, path); err != nil {
		return fail(err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// Load reads, migrates and validates a project file.
func Load(path string) (*model.Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("Die Projektdatei %s gibt es nicht. Wurde sie verschoben oder gelöscht?", path)
		}
		return nil, fmt.Errorf("Die Projektdatei %s kann nicht gelesen werden: %w", path, err)
	}
	return decode(data, filepath.Base(path))
}

func decode(data []byte, file string) (*model.Project, error) {
	var p model.Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s ist keine gültige Frame-Studio-Projektdatei (%v)", file, err)
	}
	if err := migrate(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%s ist beschädigt: %w", file, err)
	}
	return &p, nil
}

// migrate upgrades older formats to model.FormatVersion and fills nil slices.
func migrate(p *model.Project) error {
	switch {
	case p.Version > model.FormatVersion:
		return fmt.Errorf("Das Projekt stammt aus einer neueren Frame-Studio-Version (Format %d, unterstützt bis %d). Frame Studio aktualisieren, um es zu öffnen", p.Version, model.FormatVersion)
	case p.Version < 0:
		return fmt.Errorf("Unbekanntes Dateiformat %d", p.Version)
	case p.Version == 0:
		// Files without a version field predate versioning; their layout equals version 1.
		p.Version = 1
	}
	if p.ID == "" {
		p.ID = model.NewID()
	}
	if p.Media == nil {
		p.Media = []model.MediaItem{}
	}
	if p.Markers == nil {
		p.Markers = []model.Marker{}
	}
	if p.Tracks == nil {
		p.Tracks = []model.Track{}
	}
	for i := range p.Tracks {
		if p.Tracks[i].Clips == nil {
			p.Tracks[i].Clips = []model.Clip{}
		}
	}
	return nil
}

// List returns all project files in dir, newest first.
func List(dir string) ([]Entry, error) {
	ds, err := ListDetails(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(ds))
	for i, d := range ds {
		out[i] = d.Entry
	}
	return out, nil
}

// Recent returns recently opened project paths (most recent first), skipping missing files.
func Recent() []string {
	recentMu.Lock()
	defer recentMu.Unlock()
	var out []string
	for _, p := range readRecent() {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// AddRecent moves path to the top of the recent list (persisted in $XDG_CONFIG_HOME/framestudio).
func AddRecent(path string) {
	path = absPath(path)
	recentMu.Lock()
	defer recentMu.Unlock()
	list := []string{path}
	for _, p := range readRecent() {
		if p != path {
			list = append(list, p)
		}
	}
	if len(list) > maxRecent {
		list = list[:maxRecent]
	}
	writeRecent(list)
}

// RemoveRecent drops path from the recent list, e.g. after the file was deleted.
func RemoveRecent(path string) {
	path = absPath(path)
	recentMu.Lock()
	defer recentMu.Unlock()
	list := []string{}
	for _, p := range readRecent() {
		if p != path {
			list = append(list, p)
		}
	}
	writeRecent(list)
}

func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func recentFile() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		base = dir
	}
	return filepath.Join(base, "framestudio", "recent.json"), nil
}

func readRecent() []string {
	f, err := recentFile()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(f)
	if err != nil {
		return nil
	}
	var list []string
	if json.Unmarshal(data, &list) != nil {
		return nil
	}
	return list
}

// writeRecent persists the list; a failure only loses history, so it is ignored.
func writeRecent(list []string) {
	f, err := recentFile()
	if err != nil {
		return
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil || os.MkdirAll(filepath.Dir(f), 0o755) != nil {
		return
	}
	_ = writeAtomic(f, data)
}

// SuggestPath returns a non-existing file path in dir derived from the project name.
func SuggestPath(dir, name string) string {
	base := SafeName(name)
	for n := 1; ; n++ {
		file := base
		if n > 1 {
			file = fmt.Sprintf("%s %d", base, n)
		}
		p := filepath.Join(dir, file+Extension)
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
	}
}

// SafeName turns a project name into a portable file name without extension.
func SafeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('-')
		case unicode.IsControl(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	s := strings.Trim(strings.Join(strings.Fields(b.String()), " "), ". ")
	if r := []rune(s); len(r) > 80 {
		s = strings.TrimRight(string(r[:80]), ". ")
	}
	if s == "" {
		s = "Unbenanntes Projekt"
	}
	return s
}

// sortNewest orders entries by modification time, newest first, then by name.
func sortNewest(ds []Details) {
	sort.SliceStable(ds, func(i, j int) bool {
		if !ds[i].Modified.Equal(ds[j].Modified) {
			return ds[i].Modified.After(ds[j].Modified)
		}
		return strings.ToLower(ds[i].Name) < strings.ToLower(ds[j].Name)
	})
}
