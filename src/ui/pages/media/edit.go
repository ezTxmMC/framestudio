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

// targetTrack picks the track for a clip kind: the preferred track if it accepts the
// kind and is unlocked, otherwise the first such track. A missing track is created.
func targetTrack(p *model.Project, kind model.ClipKind, preferred string) (string, error) {
	if t, _ := p.Track(preferred); t != nil && t.Kind.Accepts(kind) && !t.Locked {
		return t.ID, nil
	}
	tk := model.TrackVideo
	if !kind.IsVisual() {
		tk = model.TrackAudio
	}
	for _, i := range p.TrackIndices(tk) {
		if !p.Tracks[i].Locked {
			return p.Tracks[i].ID, nil
		}
	}
	if len(p.TrackIndices(tk)) > 0 {
		if tk == model.TrackAudio {
			return "", errors.New("Alle Audiospuren sind gesperrt. Eine Spur entsperren und erneut einfügen")
		}
		return "", errors.New("Alle Videospuren sind gesperrt. Eine Spur entsperren und erneut einfügen")
	}
	return p.AddTrack(tk).ID, nil
}

// placeMedia overwrites media m onto the timeline at frame at. Stills get five seconds;
// a video with sound also gets its audio clip on the first audio track. It returns the
// ID of the main clip and its end frame.
func placeMedia(p *model.Project, m model.MediaItem, at int, preferredTrack string) (string, int, error) {
	fps := max(p.Settings.FPS, 1)
	c := p.ClipFromMedia(m, max(at, 0), stillSeconds*fps)
	tid, err := targetTrack(p, m.Kind, preferredTrack)
	if err != nil {
		return "", 0, err
	}
	if err := p.PlaceClip(tid, c); err != nil {
		return "", 0, err
	}
	if m.Kind == model.KindVideo && m.HasAudio {
		a := model.NewClip(model.KindAudio, m.Name, c.Start, c.Length)
		a.MediaID = m.ID
		a.Color = model.RGBA{R: 0x3D, G: 0x9A, B: 0x5B, A: 0xFF}
		aid, err := targetTrack(p, model.KindAudio, "")
		if err != nil {
			return "", 0, err
		}
		if err := p.PlaceClip(aid, a); err != nil {
			return "", 0, err
		}
	}
	return c.ID, c.End(), nil
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
}

// planImport expands folders (top level only, sorted), skips unsupported files and
// files the project already contains.
func planImport(paths []string, existing map[string]bool) importPlan {
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
			plan.files = append(plan.files, abs)
		}
		seen[abs] = true
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			add(p)
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			plan.unsupported = append(plan.unsupported, filepath.Base(p))
			continue
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				if _, ok := media.KindForPath(e.Name()); ok {
					names = append(names, e.Name())
				}
			}
		}
		sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
		for _, n := range names {
			add(filepath.Join(p, n))
		}
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
