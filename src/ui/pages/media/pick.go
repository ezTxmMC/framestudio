package media

import (
	"errors"
	"os"
	"strings"

	"github.com/rymdport/portal/filechooser"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
)

// pickRequest describes one file or folder choice.
type pickRequest struct {
	title   string
	accept  string // confirm button label
	folders bool   // choose folders instead of files
	multi   bool   // allow several
	dir     string // suggested start folder
}

// pickFunc asks the user for paths. It blocks and runs off the UI thread. It returns no
// paths and no error when the user cancelled, and an error when the system dialog is
// unavailable, in which case the page falls back to its own browser dialog.
type pickFunc func(req pickRequest) ([]string, error)

// portalPick uses the file chooser of the desktop (xdg-desktop-portal).
func portalPick(req pickRequest) ([]string, error) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" && os.Getenv("XDG_RUNTIME_DIR") == "" {
		return nil, errors.New("kein Desktop-Portal verfügbar")
	}
	opts := &filechooser.OpenFileOptions{
		AcceptLabel:   req.accept,
		Multiple:      req.multi,
		Directory:     req.folders,
		CurrentFolder: req.dir,
	}
	if !req.folders {
		opts.Filters = mediaFilters()
		opts.CurrentFilter = opts.Filters[0]
	}
	uris, err := filechooser.OpenFile("", req.title, opts)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, u := range uris {
		if p, ok := strings.CutPrefix(u, "file://"); ok && p != "" {
			paths = append(paths, p)
		}
	}
	if len(uris) > 0 && len(paths) == 0 {
		return nil, errors.New("das Portal lieferte keine lokalen Dateien")
	}
	return paths, nil
}

// mediaFilters groups the supported extensions for the system file chooser: all media
// first, then Video, Bild and Audio. Patterns are case-sensitive, so both cases are given.
func mediaFilters() []*filechooser.Filter {
	all := &filechooser.Filter{Name: "Alle Medien"}
	groups := []*filechooser.Filter{{Name: "Video"}, {Name: "Bild"}, {Name: "Audio"}}
	kinds := []model.ClipKind{model.KindVideo, model.KindImage, model.KindAudio}
	for _, ext := range media.Extensions() {
		k, _ := media.KindForPath(ext)
		rules := []filechooser.Rule{
			{Type: filechooser.GlobPattern, Pattern: "*" + ext},
			{Type: filechooser.GlobPattern, Pattern: "*" + strings.ToUpper(ext)},
		}
		all.Rules = append(all.Rules, rules...)
		for i, gk := range kinds {
			if gk == k {
				groups[i].Rules = append(groups[i].Rules, rules...)
			}
		}
	}
	return append([]*filechooser.Filter{all}, groups...)
}
