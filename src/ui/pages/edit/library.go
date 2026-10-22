package edit

import (
	"fmt"
	"image"
	"os"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

const thumbW, thumbH = 64, 36

// library is the left panel: project media and generators, both insertable at the playhead.
type library struct {
	pg      *Page
	tabs    *container.AppTabs
	content fyne.CanvasObject

	list      *fyne.Container
	empty     fyne.CanvasObject
	rows      map[string]*row
	sig       string
	thumbs    map[string]image.Image
	loading   map[string]bool
	failed    bool // a thumbnail could not be loaded; retried when the page is shown again
	insertBtn *widget.Button
	appendBtn *widget.Button

	genRows []*row
	gen     int
}

func newLibrary(pg *Page) *library {
	l := &library{pg: pg, rows: map[string]*row{}, thumbs: map[string]image.Image{}, loading: map[string]bool{}}

	l.list = container.NewVBox()
	l.insertBtn = widget.NewButtonWithIcon("Am Playhead einfügen", theme.ContentAddIcon(), func() { l.insertMedia(false) })
	l.appendBtn = widget.NewButton("Ans Ende anhängen", func() { l.insertMedia(true) })
	importBtn := widget.NewButtonWithIcon("Medien importieren", theme.FolderOpenIcon(), func() {
		if pg.ctx.Navigate != nil {
			pg.ctx.Navigate(page.Media)
		}
	})
	importBtn.Importance = widget.LowImportance
	emptyText := widget.NewLabel("Noch keine Medien im Projekt.\nDateien auf der Seite Medien importieren\noder ins Fenster ziehen.")
	emptyText.Alignment = fyne.TextAlignCenter
	emptyText.Importance = widget.LowImportance
	l.empty = container.NewCenter(container.NewVBox(emptyText, container.NewCenter(importBtn)))
	mediaTab := container.NewBorder(nil,
		container.New(layout.NewCustomPaddedLayout(4, 0, 0, 0), container.NewGridWithColumns(2, l.insertBtn, l.appendBtn)),
		nil, nil, container.NewStack(l.empty, container.NewVScroll(l.list)))

	genList := container.NewVBox()
	for i, g := range generators {
		r := newRow(itemLayout(generatorTile(g.ID), g.Name, g.Hint),
			func() { l.selectGenerator(i) }, func() { l.selectGenerator(i); pg.insertGenerator(g.ID) })
		l.genRows = append(l.genRows, r)
		genList.Add(r)
	}
	genBtn := widget.NewButtonWithIcon("Am Playhead einfügen", theme.ContentAddIcon(), func() {
		pg.insertGenerator(generators[l.gen].ID)
	})
	l.selectGenerator(0)
	genTab := container.NewBorder(nil,
		container.New(layout.NewCustomPaddedLayout(4, 0, 0, 0), genBtn), nil, nil, container.NewVScroll(genList))

	l.tabs = container.NewAppTabs(
		container.NewTabItem("Medien", container.New(layout.NewCustomPaddedLayout(6, 0, 0, 0), mediaTab)),
		container.NewTabItem("Generatoren", container.New(layout.NewCustomPaddedLayout(6, 0, 0, 0), genTab)),
	)
	l.content = container.NewThemeOverride(l.tabs, smallTheme{studiotheme.New()})
	l.tabs.OnSelected = func(*container.TabItem) { l.content.Refresh() }
	l.update(session.ChangeProject | session.ChangeSelection)
	return l
}

func (l *library) mediaTabActive() bool { return l.tabs.SelectedIndex() == 0 }

func (l *library) selectGenerator(i int) {
	l.gen = i
	for j, r := range l.genRows {
		r.SetSelected(i == j)
	}
}

func (l *library) insertMedia(atEnd bool) {
	if id := l.pg.ctx.Session.SelectedMedia; id != "" {
		l.pg.insertMedia(id, atEnd)
	}
}

func (l *library) update(c session.Change) {
	s := l.pg.ctx.Session
	if c&session.ChangeProject != 0 {
		var b strings.Builder
		for _, m := range s.Project.Media {
			fmt.Fprintf(&b, "%s|%s|%s|%d;", m.ID, m.Name, m.Path, m.Duration)
		}
		if sig := b.String(); sig != l.sig {
			l.sig = sig
			l.rebuild()
		}
	}
	if c&(session.ChangeProject|session.ChangeSelection) != 0 {
		for id, r := range l.rows {
			r.SetSelected(id == s.SelectedMedia)
		}
		if s.SelectedMedia == "" {
			l.insertBtn.Disable()
			l.appendBtn.Disable()
		} else {
			l.insertBtn.Enable()
			l.appendBtn.Enable()
		}
	}
}

// retryThumbs rebuilds the list if a thumbnail failed earlier, e.g. while a drive was
// unmounted.
func (l *library) retryThumbs() {
	if l.failed {
		l.failed = false
		l.rebuild()
	}
}

func (l *library) rebuild() {
	s := l.pg.ctx.Session
	l.rows = map[string]*row{}
	l.list.Objects = nil
	for _, m := range s.Project.Media {
		id := m.ID
		r := newRow(itemLayout(l.thumb(m), m.Name, mediaMeta(s.Project, m)),
			func() { s.SelectMedia(id) },
			func() { s.SelectMedia(id); l.pg.insertMedia(id, false) })
		l.rows[id] = r
		l.list.Add(r)
	}
	if len(s.Project.Media) == 0 {
		l.empty.Show()
	} else {
		l.empty.Hide()
	}
	l.list.Refresh()
	if l.content != nil {
		l.content.Refresh() // applies the small theme to the new rows
	}
}

// thumb returns a poster tile, loading the thumbnail in the background on first use.
func (l *library) thumb(m model.MediaItem) fyne.CanvasObject {
	bg := canvas.NewRectangle(studiotheme.Lane)
	bg.CornerRadius = 2
	bg.SetMinSize(fyne.NewSize(thumbW, thumbH))
	icon := widget.NewIcon(kindIcon(m.Kind))
	key := m.ID + "|" + m.Path // a relinked item gets a new poster
	img := canvas.NewImageFromImage(l.thumbs[key])
	img.FillMode = canvas.ImageFillContain
	if img.Image != nil {
		icon.Hide()
	}
	cache := l.pg.ctx.Media
	if img.Image == nil && cache != nil && !l.loading[key] {
		l.loading[key] = true
		post := l.pg.post
		go func() {
			t, err := cache.Thumbnail(m, thumbW*2, thumbH*2)
			post(func() {
				if err != nil || t == nil {
					// Keep the kind icon (missing media or ffmpeg is reported elsewhere)
					// and try again on the next rebuild, e.g. once the file is back.
					delete(l.loading, key)
					l.failed = true
					return
				}
				delete(l.loading, key)
				l.thumbs[key] = t
				img.Image = t
				icon.Hide()
				img.Refresh()
			})
		}()
	}
	return container.NewStack(bg, container.NewCenter(icon), img)
}

func kindIcon(k model.ClipKind) fyne.Resource {
	switch k {
	case model.KindVideo:
		return theme.MediaVideoIcon()
	case model.KindAudio:
		return theme.MediaMusicIcon()
	}
	return theme.MediaPhotoIcon()
}

func mediaMeta(p *model.Project, m model.MediaItem) string {
	if _, err := os.Stat(m.Path); m.Path != "" && err != nil {
		return "Datei fehlt"
	}
	parts := []string{kindNames[m.Kind]}
	if m.Duration > 0 {
		parts = append(parts, p.Timecode(p.MediaFrames(m)))
	}
	if m.Width > 0 && m.Kind != model.KindAudio {
		parts = append(parts, fmt.Sprintf("%d×%d", m.Width, m.Height))
	}
	return strings.Join(parts, "  ·  ")
}

// itemLayout is a library row: tile on the left, name over a muted detail line.
func itemLayout(tile fyne.CanvasObject, name, meta string) fyne.CanvasObject {
	n := widget.NewLabel(name)
	n.Truncation = fyne.TextTruncateEllipsis
	sub := text(meta, studiotheme.Muted, 11)
	info := container.New(layout.NewCustomPaddedLayout(0, 0, 0, 0), container.NewVBox(
		layout.NewSpacer(),
		container.New(layout.NewCustomPaddedLayout(-6, -4, 0, 0), n),
		container.New(layout.NewCustomPaddedLayout(0, 0, 6, 0), sub),
		layout.NewSpacer()))
	return container.NewBorder(nil, nil, container.NewCenter(tile), nil, info)
}

// generatorTile is a tiny preview of what a generator inserts.
func generatorTile(id string) fyne.CanvasObject {
	bg := canvas.NewRectangle(studiotheme.MonitorOut)
	bg.CornerRadius = 2
	bg.SetMinSize(fyne.NewSize(thumbW, thumbH))
	switch id {
	case "color":
		bg.FillColor = studiotheme.ClipColors[5].Color
		return bg
	case "title":
		t := text("Titel", studiotheme.Text, 12)
		t.TextStyle.Bold = true
		return container.NewStack(bg, container.NewCenter(t))
	}
	bar := canvas.NewRectangle(alpha(studiotheme.Raised, 0xFF))
	bar.SetMinSize(fyne.NewSize(38, 7))
	line := canvas.NewRectangle(studiotheme.Text)
	line.SetMinSize(fyne.NewSize(24, 2))
	lower := container.NewStack(bar, container.New(layout.NewCustomPaddedLayout(0, 0, 4, 0),
		container.NewHBox(container.NewCenter(line))))
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(0, 6, 5, 0),
		container.NewVBox(layout.NewSpacer(), container.NewHBox(lower))))
}

// smallTheme tightens text and padding for the library tabs.
type smallTheme struct{ fyne.Theme }

func (t smallTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 12
	case theme.SizeNameInnerPadding:
		return 5
	}
	return t.Theme.Size(n)
}
