package media

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
)

// stillSeconds is the default length of a still placed on the timeline.
const stillSeconds = 5

// placeMedia overwrites media m onto the timeline at frame at (see model.PlaceMedia,
// shared with the edit page). Stills get five seconds. It returns the ID of the main
// clip and its end frame.
func placeMedia(p *model.Project, m model.MediaItem, at int, preferredTrack string) (string, int, error) {
	id, err := p.PlaceMedia(m, at, max(1, p.Rate().SecondsToFrames(stillSeconds)), preferredTrack)
	if err != nil {
		return "", 0, err
	}
	c, _, _ := p.Clip(id)
	return id, c.End(), nil
}

// usage counts the timeline clips that reference a media item.
func usage(p *model.Project, mediaID string) int {
	n := 0
	for _, t := range p.Tracks {
		for _, c := range t.Clips {
			if c.MediaID == mediaID {
				n++
			}
		}
	}
	return n
}

// relink points item id at a freshly probed file, keeping its ID and name.
func relink(p *model.Project, id string, probed model.MediaItem) error {
	m, ok := p.MediaByID(id)
	if !ok {
		return errors.New("Das Medium ist nicht mehr im Projekt")
	}
	if probed.Kind != m.Kind {
		return fmt.Errorf("Die neue Datei ist %s, „%s“ ist %s. Eine Datei derselben Art wählen", kindArticle(probed.Kind), m.Name, kindArticle(m.Kind))
	}
	m.Path, m.Width, m.Height = probed.Path, probed.Width, probed.Height
	m.Duration, m.FPS, m.HasAudio = probed.Duration, probed.FPS, probed.HasAudio
	return nil
}

func kindArticle(k model.ClipKind) string {
	switch k {
	case model.KindImage:
		return "ein Bild"
	case model.KindAudio:
		return "eine Audiodatei"
	}
	return "ein Video"
}

func kindLabel(k model.ClipKind) string {
	switch k {
	case model.KindImage:
		return "Bild"
	case model.KindAudio:
		return "Audio"
	}
	return "Video"
}

// importPlan is the result of expanding dropped or chosen paths into files to probe.
type importPlan struct {
	files       []string
	unsupported []string // base names
	duplicates  int      // already in the project
	truncated   int      // supported files left out beyond maxImport
}

// maxImport bounds one import, so a folder tree picked by mistake does not flood the pool.
const maxImport = 1000

// planImport expands folders (sorted, files before subfolders; with recursive also all
// visible subfolders), skips unsupported files and files the project already contains.
// It touches the file system; call it off the UI thread.
func planImport(paths []string, existing map[string]bool, recursive bool) importPlan {
	var plan importPlan
	seen := map[string]bool{}
	add := func(path string) {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		switch {
		case seen[abs]:
		case existing[abs]:
			plan.duplicates++
		default:
			if _, ok := media.KindForPath(abs); !ok {
				plan.unsupported = append(plan.unsupported, filepath.Base(abs))
				break
			}
			if len(plan.files) >= maxImport {
				plan.truncated++
				break
			}
			plan.files = append(plan.files, abs)
		}
		seen[abs] = true
	}
	var walk func(dir string, top bool)
	walk = func(dir string, top bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if top {
				plan.unsupported = append(plan.unsupported, filepath.Base(dir))
			}
			return
		}
		var names, dirs []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if e.IsDir() { // symlinked folders are not followed (no loops)
				dirs = append(dirs, e.Name())
				continue
			}
			if _, ok := media.KindForPath(e.Name()); ok {
				names = append(names, e.Name())
			}
		}
		byName := func(s []string) {
			sort.Slice(s, func(i, j int) bool { return strings.ToLower(s[i]) < strings.ToLower(s[j]) })
		}
		byName(names)
		for _, n := range names {
			add(filepath.Join(dir, n))
		}
		if recursive {
			byName(dirs)
			for _, d := range dirs {
				walk(filepath.Join(dir, d), false)
			}
		}
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			add(p)
			continue
		}
		walk(p, true)
	}
	return plan
}

// importSummary describes skipped files and probe errors, or "" when all went well.
func importSummary(plan importPlan, failures []string) string {
	var b strings.Builder
	if len(failures) > 0 {
		fmt.Fprintf(&b, "%d %s konnten nicht gelesen werden:\n", len(failures), plural(len(failures), "Datei", "Dateien"))
		for _, f := range limit(failures, 8) {
			b.WriteString("• " + f + "\n")
		}
	}
	if len(plan.unsupported) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%d %s nicht unterstützt:\n", len(plan.unsupported), plural(len(plan.unsupported), "Datei wird", "Dateien werden"))
		for _, f := range limit(plan.unsupported, 8) {
			b.WriteString("• " + f + "\n")
		}
		b.WriteString("Unterstützt werden Bilder (PNG, JPEG, GIF, BMP, WebP, TIFF), Videos (MP4, MOV, MKV, WebM, AVI) und Audio (WAV, MP3, FLAC, OGG, M4A, AAC, Opus).\n")
	}
	if plan.truncated > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Pro Import werden höchstens %d Dateien übernommen, %d weitere wurden ausgelassen. Unterordner einzeln importieren.\n", maxImport, plan.truncated)
	}
	return strings.TrimSpace(b.String())
}

func limit(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(s[:n:n], fmt.Sprintf("… und %d weitere", len(s)-n))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
