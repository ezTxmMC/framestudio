// Package store saves and loads Frame Studio projects (*.fsproj, JSON) and keeps the
// list of recent projects.
//
// OWNER: shell agent. Exported signatures in this file are a contract; do not change them.
package store

import (
	"errors"
	"time"

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

// ProjectsDir is the default library folder ($XDG_DATA_HOME/framestudio/projects), created on demand.
func ProjectsDir() (string, error) { return "", errors.New("TODO") }

// Save writes p atomically (temp file + rename) to path.
func Save(p *model.Project, path string) error { return errors.New("TODO") }

// Load reads, migrates and validates a project file.
func Load(path string) (*model.Project, error) { return nil, errors.New("TODO") }

// List returns all project files in dir, newest first.
func List(dir string) ([]Entry, error) { return nil, errors.New("TODO") }

// Recent returns recently opened project paths (most recent first), skipping missing files.
func Recent() []string { return nil }

// AddRecent moves path to the top of the recent list (persisted in $XDG_CONFIG_HOME/framestudio).
func AddRecent(path string) {}

// SuggestPath returns a non-existing file path in dir derived from the project name.
func SuggestPath(dir, name string) string { return "" }
