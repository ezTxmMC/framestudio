package media

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// browser is the media storage list of the "Dateien" panel: folders and supported
// files of one directory. Clicking a folder opens it, clicking a file imports it.
type browser struct {
	p       *Page
	dir     string
	entries []browserEntry
	list    *widget.List
	where   *widget.Label
	up      *widget.Button
}

type browserEntry struct {
	name, path string
	dir        bool
	kind       model.ClipKind
}

func newBrowser(p *Page) *browser {
	b := &browser{p: p}
	if home, err := os.UserHomeDir(); err == nil {
		b.dir = home
	}
	if wd, err := os.Getwd(); b.dir == "" && err == nil {
		b.dir = wd
	}
	b.where = widget.NewLabel("")
	b.where.Truncation = fyne.TextTruncateEllipsis
	b.where.TextStyle = fyne.TextStyle{Bold: true}
	b.up = kit.ToolButton(theme.MoveUpIcon(), func() { b.open(filepath.Dir(b.dir)) })
	b.list = widget.NewList(
		func() int { return len(b.entries) },
		func() fyne.CanvasObject {
			name := widget.NewLabel("")
			name.Truncation = fyne.TextTruncateEllipsis
			done := widget.NewIcon(theme.ConfirmIcon())
			return container.NewBorder(nil, nil, widget.NewIcon(theme.FolderIcon()), done, name)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(b.entries) {
				return
			}
			e := b.entries[i]
			row := o.(*fyne.Container)
			name := row.Objects[0].(*widget.Label)
			icon := row.Objects[1].(*widget.Icon)
			done := row.Objects[2].(*widget.Icon)
			name.SetText(e.name)
			if e.dir {
				icon.SetResource(theme.FolderIcon())
			} else {
				icon.SetResource(kindIcon(e.kind))
			}
			done.Hidden = e.dir || !b.imported(e.path)
			done.SetResource(theme.NewColoredResource(theme.ConfirmIcon(), theme.ColorNamePrimary))
		})
	b.list.OnSelected = func(i widget.ListItemID) {
		b.list.UnselectAll()
		if i >= len(b.entries) {
			return
		}
		if e := b.entries[i]; e.dir {
			b.open(e.path)
		} else {
			b.importOrSelect(e.path)
		}
	}
	b.reload()
	return b
}

func (b *browser) content() fyne.CanvasObject {
	hint := canvas.NewText("Ordner öffnen, Datei anklicken importiert", studiotheme.Faint)
	hint.TextSize = 11
	return container.NewBorder(container.NewBorder(nil, nil, b.up, nil, b.where), container.NewPadded(hint), nil, nil, b.list)
}

func (b *browser) imported(path string) bool {
	for _, m := range b.p.ctx.Session.Project.Media {
		if m.Path == path {
			return true
		}
	}
	return false
}

// importOrSelect imports a file, or selects it in the pool when it is already there.
func (b *browser) importOrSelect(path string) {
	for _, m := range b.p.ctx.Session.Project.Media {
		if m.Path == path {
			b.p.ctx.Session.SelectMedia(m.ID)
			return
		}
	}
	b.p.importPaths([]string{path})
}

// open shows directory dir; unreadable directories leave the view unchanged.
func (b *browser) open(dir string) {
	entries, err := readDir(dir)
	if err != nil {
		b.p.showErr(err)
		return
	}
	b.dir, b.entries = dir, entries
	b.update()
}

func (b *browser) reload() {
	entries, err := readDir(b.dir)
	if err != nil {
		entries = nil
	}
	b.entries = entries
	b.update()
}

func (b *browser) update() {
	name := filepath.Base(b.dir)
	if home, err := os.UserHomeDir(); err == nil && b.dir == home {
		name = "Persönlicher Ordner"
	}
	b.where.SetText(name)
	if filepath.Dir(b.dir) == b.dir {
		b.up.Disable()
	} else {
		b.up.Enable()
	}
	if b.list.Size().Height > 0 { // the scroller exists once the list is laid out
		b.list.ScrollToTop()
	}
	b.list.Refresh()
}

// readDir lists visible folders and supported media files, folders first.
func readDir(dir string) ([]browserEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, &os.PathError{Op: "Ordner kann nicht geöffnet werden", Path: dir, Err: err}
	}
	var out []browserEntry
	for _, d := range des {
		if strings.HasPrefix(d.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, d.Name())
		isDir := d.IsDir()
		if d.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(path); err == nil {
				isDir = st.IsDir()
			}
		}
		if isDir {
			out = append(out, browserEntry{name: d.Name(), path: path, dir: true})
			continue
		}
		if k, ok := media.KindForPath(d.Name()); ok {
			out = append(out, browserEntry{name: d.Name(), path: path, kind: k})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].dir != out[j].dir {
			return out[i].dir
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out, nil
}
