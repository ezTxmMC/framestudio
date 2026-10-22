package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"framestudio-proto/src/model"
)

// Details is an Entry with the extra facts the project manager shows on a card.
type Details struct {
	Entry
	Width, Height int
	Clips         int
}

// Describe reads the summary of one project file.
func Describe(path string) (Details, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Details{}, fmt.Errorf("Die Projektdatei %s gibt es nicht", path)
	}
	p, err := Load(path)
	if err != nil {
		return Details{}, err
	}
	return detailsOf(p, path, fi), nil
}

func detailsOf(p *model.Project, path string, fi os.FileInfo) Details {
	clips := 0
	for _, t := range p.Tracks {
		clips += len(t.Clips)
	}
	name := p.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return Details{
		Entry: Entry{Path: path, Name: name, Modified: fi.ModTime(), Duration: p.Duration(), FPS: p.Settings.FPS},
		Width: p.Settings.Width, Height: p.Settings.Height, Clips: clips,
	}
}

// ListDetails is List with card details; unreadable or broken files are skipped.
func ListDetails(dir string) ([]Details, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("Der Projektordner %s kann nicht gelesen werden: %w", dir, err)
	}
	out := []Details{}
	for _, de := range des {
		n := de.Name()
		if de.IsDir() || strings.HasPrefix(n, ".") || !strings.EqualFold(filepath.Ext(n), Extension) {
			continue
		}
		if d, err := Describe(filepath.Join(dir, n)); err == nil {
			out = append(out, d)
		}
	}
	sortNewest(out)
	return out, nil
}
