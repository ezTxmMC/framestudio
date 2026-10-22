// Package media is the "media" workspace page.
//
// OWNER: media page agent. New(ctx) is the contract; do not change its signature.
package media

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

// Kind filter choices of the pool header.
var kindChoices = []struct {
	label string
	kind  model.ClipKind
}{
	{"Alle Typen", ""},
	{"Video", model.KindVideo},
	{"Bild", model.KindImage},
	{"Audio", model.KindAudio},
}

// Page is the media page: file browser, media pool and clip inspector.
type Page struct {
	ctx    *page.Context
	unsub  func()
	closed bool
	// spawn runs background work; tests replace it to stay on one goroutine.
	spawn func(func())

	view       []model.MediaItem // pool items after search and kind filter
	query      string
	kindFilter model.ClipKind
	missing    map[string]bool        // media ID -> file not found
	thumbs     map[string]image.Image // "id|path" -> pool thumbnail (nil = failed)
	loading    map[string]bool
	sem        chan struct{} // bounds concurrent thumbnail decodes

	grid     *widget.GridWrap
	search   *widget.Entry
	kindSel  *widget.Select
	count    *canvas.Text
	tools    *fyne.Container
	empty    *fyne.Container
	emptyMsg *canvas.Text
	emptySub *canvas.Text
	emptyBtn *widget.Button

	importing   bool
	importBtn   *widget.Button
	folderBtn   *widget.Button
	progress    *widget.ProgressBar
	progressMsg *canvas.Text
	progressBox *fyne.Container

	files   *browser
	insp    *inspector
	content fyne.CanvasObject
}

func New(ctx *page.Context) page.Page {
	p := &Page{
		ctx:     ctx,
		spawn:   func(f func()) { go f() },
		missing: map[string]bool{},
		thumbs:  map[string]image.Image{},
		loading: map[string]bool{},
		sem:     make(chan struct{}, 3),
	}
	p.content = p.build()
	p.unsub = ctx.Session.Subscribe(p.onChange)
	p.refreshPool()
	p.insp.show()
	return p
}

func (p *Page) Content() fyne.CanvasObject { return p.content }

func (p *Page) Activate() {
	p.refreshPool()
	p.files.reload()
	p.insp.show()
}

func (p *Page) Deactivate() {}

func (p *Page) Close() {
	p.closed = true
	if p.unsub != nil {
		p.unsub()
		p.unsub = nil
	}
}

// TypedKey: Delete removes, Enter inserts, arrows move the pool selection.
func (p *Page) TypedKey(k *fyne.KeyEvent) bool {
	switch k.Name {
	case fyne.KeyDelete, fyne.KeyBackspace:
		if m, ok := p.selected(); ok {
			p.confirmRemove(m)
			return true
		}
	case fyne.KeyReturn, fyne.KeyEnter:
		if m, ok := p.selected(); ok {
			p.insert(m, false)
			return true
		}
	case fyne.KeyLeft:
		return p.moveSelection(-1)
	case fyne.KeyRight:
		return p.moveSelection(1)
	case fyne.KeyUp:
		return p.moveSelection(-max(p.grid.ColumnCount(), 1))
	case fyne.KeyDown:
		return p.moveSelection(max(p.grid.ColumnCount(), 1))
	}
	return false
}

// Dropped imports files and folders dropped onto the window.
func (p *Page) Dropped(uris []fyne.URI) {
	var paths []string
	for _, u := range uris {
		if u != nil && u.Scheme() == "file" {
			paths = append(paths, u.Path())
		}
	}
	if len(paths) > 0 {
		p.importPaths(paths)
	}
}

func (p *Page) build() fyne.CanvasObject {
	p.files = newBrowser(p)
	p.insp = newInspector(p)

	p.importBtn = widget.NewButtonWithIcon("Importieren", theme.ContentAddIcon(), p.showImportDialog)
	p.importBtn.Importance = widget.HighImportance
	p.folderBtn = widget.NewButtonWithIcon("Ordner importieren", theme.FolderOpenIcon(), p.showFolderDialog)
	p.progress = widget.NewProgressBar()
	p.progressMsg = canvas.NewText("", studiotheme.Muted)
	p.progressMsg.TextSize = 12
	p.progressBox = container.NewVBox(p.progressMsg, p.progress)
	p.progressBox.Hide()
	left := kit.Panel("Dateien", nil, container.NewBorder(
		container.NewVBox(p.importBtn, p.folderBtn, spacer(4)),
		p.progressBox, nil, nil, p.files.content()))

	return container.New(layout.NewCustomPaddedLayout(4, 4, 4, 4), container.NewBorder(nil, nil,
		fixedWidth(250, left), fixedWidth(330, p.insp.panel()), p.buildPool()))
}

func (p *Page) buildPool() fyne.CanvasObject {
	p.search = widget.NewEntry()
	p.search.SetPlaceHolder("Suchen")
	p.search.ActionItem = widget.NewIcon(theme.SearchIcon())
	p.search.OnChanged = func(s string) {
		p.query = strings.ToLower(strings.TrimSpace(s))
		p.refreshPool()
	}
	labels := make([]string, len(kindChoices))
	for i, c := range kindChoices {
		labels[i] = c.label
	}
	p.kindSel = widget.NewSelect(labels, nil)
	p.kindSel.PlaceHolder = "Typ"
	p.kindSel.Selected = labels[0]
	p.kindSel.OnChanged = func(s string) {
		for _, c := range kindChoices {
			if c.label == s {
				p.kindFilter = c.kind
			}
		}
		p.refreshPool()
	}
	p.count = canvas.NewText("", studiotheme.Muted)
	p.count.TextSize = 12

	p.grid = widget.NewGridWrap(
		func() int { return len(p.view) },
		func() fyne.CanvasObject { return newCard() },
		func(i widget.GridWrapItemID, o fyne.CanvasObject) {
			if i < 0 || i >= len(p.view) {
				return
			}
			m := p.view[i]
			c := o.(*card)
			c.onTap = func() { p.ctx.Session.SelectMedia(m.ID) }
			c.onSecondary = func(pos fyne.Position) { p.showMenu(m.ID, pos) }
			c.set(m, p.fps(), p.thumbFor(m), m.ID == p.ctx.Session.SelectedMedia, p.missing[m.ID])
		})

	p.emptyMsg = canvas.NewText("", studiotheme.Text)
	p.emptyMsg.TextSize = 14
	p.emptyMsg.Alignment = fyne.TextAlignCenter
	p.emptySub = canvas.NewText("", studiotheme.Muted)
	p.emptySub.TextSize = 12
	p.emptySub.Alignment = fyne.TextAlignCenter
	icon := canvas.NewImageFromResource(theme.NewColoredResource(theme.MediaVideoIcon(), theme.ColorNameDisabled))
	icon.SetMinSize(fyne.NewSize(48, 48))
	p.emptyBtn = widget.NewButtonWithIcon("Importieren", theme.ContentAddIcon(), p.showImportDialog)
	p.empty = container.NewCenter(container.NewVBox(container.NewCenter(icon), p.emptyMsg, p.emptySub,
		spacer(4), container.NewCenter(p.emptyBtn)))

	p.count.Alignment = fyne.TextAlignTrailing
	p.tools = container.NewHBox(fixedWidth(80, container.NewVBox(layout.NewSpacer(), p.count, layout.NewSpacer())), spacer(4), fixedWidth(190, p.search), fixedWidth(118, p.kindSel))
	body := container.NewStack(p.grid, p.empty)
	if !media.FFmpegAvailable() {
		body = container.NewBorder(ffmpegNotice(), nil, nil, nil, body)
	}
	return kit.Panel("Media Pool", p.tools, body)
}

// ffmpegNotice explains why video and audio cannot be used.
func ffmpegNotice() fyne.CanvasObject {
	bg := canvas.NewRectangle(studiotheme.Raised)
	bg.CornerRadius = 3
	icon := widget.NewIcon(theme.WarningIcon())
	msg := widget.NewLabel("ffmpeg wurde nicht gefunden. Video und Audio können erst nach der Installation von ffmpeg importiert werden.")
	msg.Wrapping = fyne.TextWrapWord
	return container.NewStack(bg, container.NewBorder(nil, nil, icon, nil, msg))
}

func (p *Page) fps() int { return max(p.ctx.Session.FPS(), 1) }

// onChange keeps the pool and inspector in step with the session.
func (p *Page) onChange(c session.Change) {
	if p.closed {
		return
	}
	if c&session.ChangeProject != 0 {
		p.refreshPool()
	} else if c&session.ChangeSelection != 0 {
		p.grid.Refresh()
	}
	if c&(session.ChangeProject|session.ChangeSelection) != 0 {
		p.insp.show()
		p.files.list.Refresh()
	}
}

// refreshPool rebuilds the filtered view and the missing-file flags.
func (p *Page) refreshPool() {
	all := p.ctx.Session.Project.Media
	p.view = p.view[:0]
	for _, m := range all {
		if p.kindFilter != "" && m.Kind != p.kindFilter {
			continue
		}
		if p.query != "" && !strings.Contains(strings.ToLower(m.Name), p.query) {
			continue
		}
		p.view = append(p.view, m)
	}
	live := map[string]bool{}
	for _, m := range all {
		_, err := os.Stat(m.Path)
		p.missing[m.ID] = err != nil
		live[m.ID+"|"+m.Path] = true
	}
	for k := range p.thumbs {
		if !live[k] {
			delete(p.thumbs, k)
		}
	}
	switch {
	case len(all) == 0:
		p.count.Text = ""
		p.emptyMsg.Text = "Noch keine Medien im Projekt"
		p.emptySub.Text = "Dateien importieren oder in das Fenster ziehen"
		p.emptyBtn.Show()
		p.empty.Show()
	case len(p.view) == 0:
		p.count.Text = fmt.Sprintf("0 von %d", len(all))
		p.emptyMsg.Text = "Keine Treffer"
		p.emptySub.Text = "Suchbegriff oder Typfilter ändern"
		p.emptyBtn.Hide()
		p.empty.Show()
	default:
		if len(p.view) == len(all) {
			p.count.Text = fmt.Sprintf("%d %s", len(all), plural(len(all), "Medium", "Medien"))
		} else {
			p.count.Text = fmt.Sprintf("%d von %d", len(p.view), len(all))
		}
		p.empty.Hide()
	}
	p.count.Refresh()
	p.empty.Refresh()
	p.grid.Refresh()
}

// thumbFor returns the pool thumbnail of m, loading it in the background on first use.
func (p *Page) thumbFor(m model.MediaItem) image.Image {
	key := m.ID + "|" + m.Path
	if img, ok := p.thumbs[key]; ok {
		return img
	}
	if p.loading[key] || p.missing[m.ID] || p.ctx.Media == nil {
		return nil
	}
	p.loading[key] = true
	cache := p.ctx.Media
	p.spawn(func() {
		p.sem <- struct{}{}
		img, err := cache.Thumbnail(m, 2*(cardW-2*cardPad), 2*thumbH)
		<-p.sem
		if err != nil {
			img = nil
		}
		kit.Do(func() {
			delete(p.loading, key)
			if p.closed {
				return
			}
			p.thumbs[key] = img
			for i, v := range p.view {
				if v.ID == m.ID {
					p.grid.RefreshItem(i)
				}
			}
		})
	})
	return nil
}

func (p *Page) selected() (model.MediaItem, bool) {
	m, ok := p.ctx.Session.Project.MediaByID(p.ctx.Session.SelectedMedia)
	if !ok {
		return model.MediaItem{}, false
	}
	return *m, true
}

func (p *Page) moveSelection(delta int) bool {
	if len(p.view) == 0 {
		return false
	}
	cur := -1
	for i, m := range p.view {
		if m.ID == p.ctx.Session.SelectedMedia {
			cur = i
		}
	}
	next := 0
	if cur >= 0 {
		next = min(max(cur+delta, 0), len(p.view)-1)
	}
	p.ctx.Session.SelectMedia(p.view[next].ID)
	p.grid.ScrollTo(next)
	return true
}

func (p *Page) showMenu(id string, pos fyne.Position) {
	m, ok := p.ctx.Session.Project.MediaByID(id)
	if !ok {
		return
	}
	item := *m
	menu := fyne.NewMenu("",
		fyne.NewMenuItem("In Timeline einfügen", func() { p.insert(item, false) }),
		fyne.NewMenuItem("Ans Ende anhängen", func() { p.insert(item, true) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Neu verknüpfen…", func() { p.showRelinkDialog(item) }),
		fyne.NewMenuItem("Entfernen…", func() { p.confirmRemove(item) }),
	)
	widget.ShowPopUpMenuAtPosition(menu, p.ctx.Window.Canvas(), pos)
}

// insert places m at the playhead (or appends it at the timeline end) as one edit,
// selects the new clip and moves the playhead behind it.
func (p *Page) insert(m model.MediaItem, atEnd bool) {
	s := p.ctx.Session
	label := "In Timeline einfügen"
	if atEnd {
		label = "Ans Ende anhängen"
	}
	var id string
	var end int
	err := s.Edit(label, func(pr *model.Project) error {
		at := s.Playhead
		if atEnd {
			at = pr.Duration()
		}
		var err error
		id, end, err = placeMedia(pr, m, at, s.SelectedTrack)
		return err
	})
	if err != nil {
		p.showErr(fmt.Errorf("„%s“ konnte nicht eingefügt werden: %w", m.Name, err))
		return
	}
	s.SelectClip(id)
	s.Seek(end)
}

func (p *Page) confirmRemove(m model.MediaItem) {
	n := usage(p.ctx.Session.Project, m.ID)
	msg := fmt.Sprintf("„%s“ wird aus dem Projekt entfernt. Es wird in keinem Clip verwendet.", m.Name)
	if n > 0 {
		msg = fmt.Sprintf("„%s“ wird aus dem Projekt entfernt.\n%d %s in der Timeline %s es und %s ebenfalls gelöscht.",
			m.Name, n, plural(n, "Clip", "Clips"), plural(n, "verwendet", "verwenden"), plural(n, "wird", "werden"))
	}
	d := dialog.NewConfirm("Medium entfernen", msg, func(ok bool) {
		if ok {
			p.remove(m.ID)
		}
	}, p.ctx.Window)
	d.SetConfirmText("Entfernen")
	d.SetDismissText("Abbrechen")
	d.Show()
}

func (p *Page) remove(id string) {
	err := p.ctx.Session.Edit("Medium entfernen", func(pr *model.Project) error { return pr.RemoveMedia(id) })
	if errors.Is(err, model.ErrLocked) {
		p.showErr(errors.New("Medium konnte nicht entfernt werden: Clips davon liegen auf einer gesperrten Spur. Spur entsperren und erneut versuchen"))
		return
	}
	if err != nil {
		p.showErr(fmt.Errorf("Medium konnte nicht entfernt werden: %w", err))
		return
	}
	if p.ctx.Media != nil {
		p.ctx.Media.Invalidate(id)
	}
}

func (p *Page) mediaFilter() storage.FileFilter {
	return storage.NewExtensionFileFilter(media.Extensions())
}

func (p *Page) startDir() fyne.ListableURI {
	if l, err := storage.ListerForURI(storage.NewFileURI(p.files.dir)); err == nil {
		return l
	}
	return nil
}

func (p *Page) showImportDialog() {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			p.showErr(err)
			return
		}
		if r == nil {
			return
		}
		path := r.URI().Path()
		_ = r.Close()
		p.importPaths([]string{path})
	}, p.ctx.Window)
	d.SetFilter(p.mediaFilter())
	d.SetLocation(p.startDir())
	d.SetTitleText("Medien importieren")
	d.SetConfirmText("Importieren")
	d.SetDismissText("Abbrechen")
	d.Resize(dialogSize(p.ctx.Window))
	d.Show()
}

func (p *Page) showFolderDialog() {
	d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil {
			p.showErr(err)
			return
		}
		if u != nil {
			p.importPaths([]string{u.Path()})
		}
	}, p.ctx.Window)
	d.SetLocation(p.startDir())
	d.SetTitleText("Ordner importieren")
	d.SetConfirmText("Importieren")
	d.SetDismissText("Abbrechen")
	d.Resize(dialogSize(p.ctx.Window))
	d.Show()
}

func dialogSize(w fyne.Window) fyne.Size {
	s := w.Canvas().Size()
	return fyne.NewSize(min(900, s.Width*0.9), min(620, s.Height*0.9))
}

// importPaths probes files (folders expand to their supported files) in the background
// and adds them to the project in one undo step.
func (p *Page) importPaths(paths []string) {
	if p.importing {
		p.showErr(errors.New("Es läuft bereits ein Import. Nach dessen Abschluss erneut importieren"))
		return
	}
	existing := map[string]bool{}
	for _, m := range p.ctx.Session.Project.Media {
		existing[m.Path] = true
	}
	plan := planImport(paths, existing)
	if len(plan.files) == 0 {
		switch msg := importSummary(plan, nil); {
		case msg != "":
			dialog.ShowInformation("Nichts importiert", msg, p.ctx.Window)
		case plan.duplicates > 0:
			dialog.ShowInformation("Nichts importiert", "Die gewählten Dateien sind bereits im Projekt.", p.ctx.Window)
		default:
			dialog.ShowInformation("Nichts importiert", "Im gewählten Ordner liegen keine unterstützten Mediendateien.", p.ctx.Window)
		}
		return
	}
	p.setImporting(true)
	p.importProgress(0, len(plan.files))
	p.spawn(func() {
		items := make([]model.MediaItem, len(plan.files))
		errs := make([]error, len(plan.files))
		for i, f := range plan.files {
			items[i], errs[i] = media.Probe(f)
			done := i + 1
			kit.Do(func() { p.importProgress(done, len(plan.files)) })
		}
		kit.Do(func() { p.finishImport(plan, items, errs) })
	})
}

func (p *Page) setImporting(on bool) {
	p.importing = on
	if on {
		p.importBtn.Disable()
		p.folderBtn.Disable()
		p.emptyBtn.Disable()
		p.progressBox.Show()
	} else {
		p.importBtn.Enable()
		p.folderBtn.Enable()
		p.emptyBtn.Enable()
		p.progressBox.Hide()
	}
}

func (p *Page) importProgress(done, total int) {
	if p.closed {
		return
	}
	p.progressMsg.Text = fmt.Sprintf("Importiere %d von %d …", min(done+1, total), total)
	p.progressMsg.Refresh()
	p.progress.SetValue(float64(done) / float64(total))
}

func (p *Page) finishImport(plan importPlan, items []model.MediaItem, errs []error) {
	p.setImporting(false)
	if p.closed {
		return
	}
	var ok []model.MediaItem
	var failures []string
	for i, err := range errs {
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		ok = append(ok, items[i])
	}
	if len(ok) > 0 {
		err := p.ctx.Session.Edit("Medien importieren", func(pr *model.Project) error {
			for _, m := range ok {
				pr.AddMedia(m)
			}
			return nil
		})
		if err != nil {
			p.showErr(err)
			return
		}
		p.ctx.Session.SelectMedia(ok[0].ID)
		for i, m := range p.view {
			if m.ID == ok[0].ID {
				p.grid.ScrollTo(i)
			}
		}
	}
	if msg := importSummary(plan, failures); msg != "" {
		title := "Import unvollständig"
		if len(ok) == 0 {
			title = "Nichts importiert"
		} else {
			msg = fmt.Sprintf("%d %s importiert.\n\n%s", len(ok), plural(len(ok), "Datei wurde", "Dateien wurden"), msg)
		}
		dialog.ShowInformation(title, msg, p.ctx.Window)
	}
}

func (p *Page) showRelinkDialog(m model.MediaItem) {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			p.showErr(err)
			return
		}
		if r == nil {
			return
		}
		path := r.URI().Path()
		_ = r.Close()
		p.relinkTo(m.ID, path)
	}, p.ctx.Window)
	d.SetFilter(p.mediaFilter())
	d.SetLocation(p.startDir())
	d.SetTitleText(fmt.Sprintf("„%s“ neu verknüpfen", m.Name))
	d.SetConfirmText("Verknüpfen")
	d.SetDismissText("Abbrechen")
	d.Resize(dialogSize(p.ctx.Window))
	d.Show()
}

// relinkTo probes path in the background and points media id at it.
func (p *Page) relinkTo(id, path string) {
	p.spawn(func() {
		probed, err := media.Probe(path)
		kit.Do(func() {
			if p.closed {
				return
			}
			if err != nil {
				p.showErr(err)
				return
			}
			if err := p.ctx.Session.Edit("Medium neu verknüpfen", func(pr *model.Project) error {
				return relink(pr, id, probed)
			}); err != nil {
				p.showErr(err)
				return
			}
			if p.ctx.Media != nil {
				p.ctx.Media.Invalidate(id)
			}
			for k := range p.thumbs {
				if strings.HasPrefix(k, id+"|") {
					delete(p.thumbs, k)
				}
			}
			p.insp.reset()
			p.refreshPool()
			p.insp.show()
		})
	})
}

func (p *Page) showErr(err error) {
	if p.ctx.ShowErr != nil {
		p.ctx.ShowErr(err)
		return
	}
	dialog.ShowError(err, p.ctx.Window)
}

// fixedWidth gives o a fixed minimum width.
func fixedWidth(w float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(widthLayout(w), o)
}

type widthLayout float32

func (l widthLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Resize(size)
		o.Move(fyne.NewPos(0, 0))
	}
}

func (l widthLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objs {
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(float32(l), h)
}

func spacer(h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(1, h))
	return r
}
