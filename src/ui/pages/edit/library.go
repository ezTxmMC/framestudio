package edit

import (
	"fmt"
	"image"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
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

	// picked holds the selected media IDs (Ctrl/Shift click); the session's
	// SelectedMedia is the primary one. anchor is where a Shift range starts.
	picked   []string
	anchor   string
	dragging bool
	dragAt   fyne.Position

	genRows []*row
	gen     int

	// offline is the media cache's offline state as last drawn (path -> missing);
	// unsubs end the cache subscriptions on close.
	offline map[string]bool
	unsubs  []func()
	closed  bool
}

func newLibrary(pg *Page) *library {
	l := &library{pg: pg, rows: map[string]*row{}, thumbs: map[string]image.Image{}, loading: map[string]bool{}}

	l.list = container.NewVBox()
	l.insertBtn = widget.NewButtonWithIcon("Einfügen", theme.ContentAddIcon(), func() { l.insertMedia(false) })
	l.appendBtn = widget.NewButton("Ans Ende", func() { l.insertMedia(true) })
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
	if cache := pg.ctx.Media; cache != nil {
		// Both callbacks run on background goroutines.
		l.unsubs = append(l.unsubs,
			cache.OnOfflineChange(func() { pg.post(l.offlineChanged) }),
			cache.OnInvalidate(func(id string) {
				pg.post(func() {
					if l.closed {
						return
					}
					for k := range l.thumbs {
						if strings.HasPrefix(k, id+"|") {
							delete(l.thumbs, k)
						}
					}
					l.rebuild()
				})
			}))
	}
	return l
}

// checkOffline refreshes the cache's offline map for the project's media off the UI
// thread; a change comes back through OnOfflineChange.
func (l *library) checkOffline() {
	cache := l.pg.ctx.Media
	if cache == nil || l.closed {
		return
	}
	items := append([]model.MediaItem(nil), l.pg.ctx.Session.Project.Media...)
	go cache.CheckOffline(items)
}

// offlineChanged rebuilds the rows when a file of this project went offline or came back.
func (l *library) offlineChanged() {
	if l.closed {
		return
	}
	for _, m := range l.pg.ctx.Session.Project.Media {
		if l.isOffline(m) != l.offline[m.Path] {
			l.rebuild()
			return
		}
	}
}

func (l *library) isOffline(m model.MediaItem) bool {
	return m.Path != "" && l.pg.ctx.Media != nil && l.pg.ctx.Media.Offline(m)
}

func (l *library) close() {
	l.closed = true
	for _, u := range l.unsubs {
		u()
	}
	l.unsubs = nil
}

func (l *library) mediaTabActive() bool { return l.tabs.SelectedIndex() == 0 }

func (l *library) selectGenerator(i int) {
	l.gen = i
	for j, r := range l.genRows {
		r.SetSelected(i == j)
	}
}

func (l *library) insertMedia(atEnd bool) {
	if ids := l.pickedIDs(); len(ids) > 0 {
		l.pg.insertMediaList(ids, atEnd)
	}
}

// pickedIDs returns the selected media in library order.
func (l *library) pickedIDs() []string {
	var out []string
	for _, m := range l.pg.ctx.Session.Project.Media {
		if contains(l.picked, m.ID) {
			out = append(out, m.ID)
		}
	}
	return out
}

// click selects media id: plain click selects only it, Ctrl toggles it, Shift selects
// the range from the last plain click.
func (l *library) click(id string, mod fyne.KeyModifier) {
	s := l.pg.ctx.Session
	switch {
	case mod&(fyne.KeyModifierControl|fyne.KeyModifierSuper) != 0:
		if contains(l.picked, id) {
			l.picked = remove(l.picked, id)
		} else {
			l.picked = append(l.picked, id)
		}
		l.anchor = id
	case mod&fyne.KeyModifierShift != 0 && l.anchor != "":
		l.picked = nil
		in := false
		for _, m := range s.Project.Media {
			edge := m.ID == id || m.ID == l.anchor
			if edge || in {
				l.picked = append(l.picked, m.ID)
			}
			if edge && id != l.anchor {
				in = !in
			}
		}
		// the clicked item becomes the primary one
		l.picked = append(remove(l.picked, id), id)
	default:
		l.picked = []string{id}
		l.anchor = id
	}
	primary := ""
	if n := len(l.picked); n > 0 {
		primary = l.picked[n-1]
	}
	s.SelectMedia(primary)
}

// drag runs an in-app media drag from row id to the timeline.
func (l *library) drag(id string, ev *fyne.DragEvent) {
	w := l.pg.ctx.Window
	if w == nil {
		return
	}
	if !l.dragging {
		l.dragging = true
		if !contains(l.picked, id) {
			l.click(id, 0)
		}
		ids := l.pickedIDs()
		label := plural(len(ids), "Medium", "Medien")
		if len(ids) == 1 {
			if m, ok := l.pg.ctx.Session.Project.MediaByID(ids[0]); ok {
				label = m.Name
			}
		}
		kit.BeginMediaDrag(w, ids, label)
	}
	l.dragAt = ev.AbsolutePosition
	kit.UpdateMediaDrag(ev.AbsolutePosition)
}

func (l *library) dragEnd() {
	if !l.dragging {
		return
	}
	l.dragging = false
	kit.EndMediaDrag(l.dragAt)
}

func remove(list []string, id string) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		if x != id {
			out = append(out, x)
		}
	}
	return out
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
			l.checkOffline()
		}
	}
	if c&(session.ChangeProject|session.ChangeSelection) != 0 {
		// follow selections made elsewhere (media page, removed media)
		var kept []string
		for _, id := range l.picked {
			if _, ok := s.Project.MediaByID(id); ok {
				kept = append(kept, id)
			}
		}
		l.picked = kept
		if s.SelectedMedia == "" {
			l.picked = nil
		} else if !contains(l.picked, s.SelectedMedia) {
			l.picked = []string{s.SelectedMedia}
			l.anchor = s.SelectedMedia
		}
		for id, r := range l.rows {
			r.SetSelected(contains(l.picked, id))
		}
		if len(l.picked) == 0 {
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
	l.offline = map[string]bool{}
	for _, m := range s.Project.Media {
		id := m.ID
		off := l.isOffline(m)
		l.offline[m.Path] = off
		r := newRow(itemLayout(l.thumb(m), m.Name, mediaMeta(s.Project, m, off)),
			nil, func() { l.click(id, 0); l.pg.insertMedia(id, false) })
		r.OnTapMod = func(mod fyne.KeyModifier) { l.click(id, mod) }
		r.OnDrag = func(ev *fyne.DragEvent) { l.drag(id, ev) }
		r.OnDragEnd = l.dragEnd
		r.SetSelected(contains(l.picked, id))
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

// mediaMeta is a library row's detail line; offline comes from the media cache's offline
// map so the UI thread never touches the file system.
func mediaMeta(p *model.Project, m model.MediaItem, offline bool) string {
	if offline {
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

// compactTheme packs the inspector rows tighter than the library.
type compactTheme struct{ fyne.Theme }

func (t compactTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 12
	case theme.SizeNameInnerPadding:
		return 4
	case theme.SizeNamePadding:
		return 3
	case theme.SizeNameInlineIcon:
		return 16
	}
	return t.Theme.Size(n)
}
