package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// SortOrder is how the project manager orders the library.
type SortOrder int

const (
	SortModified SortOrder = iota // newest first
	SortName                      // A–Z, ignoring case; umlauts sort with their base letter
)

// SortDetails orders ds in place; ties fall back to the other key so the order is stable.
func SortDetails(ds []Details, by SortOrder) {
	if by != SortName {
		sortNewest(ds)
		return
	}
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := foldName(ds[i].Name), foldName(ds[j].Name)
		if a != b {
			return a < b
		}
		return ds[i].Modified.After(ds[j].Modified)
	})
}

// FilterDetails returns the projects whose name or file name contains every word of
// query (case-insensitive). An empty query returns ds unchanged.
func FilterDetails(ds []Details, query string) []Details {
	words := strings.Fields(foldName(query))
	if len(words) == 0 {
		return ds
	}
	out := []Details{}
	for _, d := range ds {
		hay := foldName(d.Name + " " + filepath.Base(d.Path))
		match := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				match = false
				break
			}
		}
		if match {
			out = append(out, d)
		}
	}
	return out
}

// foldName lower-cases s and maps umlauts to their base letters so "Über" sorts with "U".
var umlauts = strings.NewReplacer("ä", "a", "ö", "o", "ü", "u", "ß", "ss")

func foldName(s string) string {
	return umlauts.Replace(strings.ToLower(s))
}
