// Package media is the "media" workspace page.
//
// OWNER: media page agent. New(ctx) is the contract; do not change its signature.
package media

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	fynelayout "fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/layout"
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
	// life ends when the page closes: queued thumbnails and a running import stop
	// instead of starting more ffmpeg/ffprobe processes for a closed project.
	life     context.Context
	stopLife context.CancelFunc
	// spawn runs background work; tests replace it to stay on one goroutine.
	spawn func(func())
	// probe inspects a file for import (media.ProbeContext); tests count calls.
	probe func(ctx context.Context, path string) (model.MediaItem, error)
	// pickFiles asks for files or folders (desktop portal); on error the page falls back
	// to its own browser dialog. Tests replace it.
	pickFiles pickFunc
	picking   bool
	fileDlg   *fileDialog // open fallback browser, for tests

	view       []model.MediaItem // pool items after search and kind filter
	query      string
	kindFilter model.ClipKind
	thumbs     map[string]image.Image // "id|path" -> pool thumbnail (nil = failed)
	loading    map[string]bool
	sem        chan struct{} // bounds concurrent thumbnail decodes

	// Pool selection: sel holds the selected media IDs in selection order, the last one
	// is the session's SelectedMedia; anchor is where Shift+click ranges start.
	sel    []string
	anchor string

	// Offline watcher: the cache's missing-file map is refreshed in the background on
	// import, on activation and every offlineEvery while the page is visible.
	offlineEvery time.Duration
	watchStop    chan struct{}
	checking     bool
	unsubOff     func()

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
	recursive   *widget.Check
	progress    *widget.ProgressBar
	progressMsg *canvas.Text
	progressBox *fyne.Container

	files   *browser
	insp    *inspector
	content fyne.CanvasObject

	// poolOnly builds just the pool with its import buttons (the detachable media pool
	// panel, see panel.go).
	poolOnly bool
	// Layout: files | (pool | inspector), see layout.go.
	outer, inner *container.Split
	filesBox     fyne.CanvasObject
	inspBox      fyne.CanvasObject
	pending      *layout.Page
	// openDir shows a folder in the file manager (media.reveal); tests replace it.
	openDir       func(dir string) error
	unsubSettings func()
}

func New(ctx *page.Context) page.Page {
	p := newPage(ctx, false)
	pages[ctx.Session] = p
	return p
}

// pages maps each session to its media page, for the global commands (UI thread only).
var pages = map[*session.Session]*Page{}

func newPage(ctx *page.Context, poolOnly bool) *Page {
	p := &Page{
		poolOnly:     poolOnly,
		openDir:      openWithSystem,
		ctx:          ctx,
		spawn:        func(f func()) { go f() },
		probe:        media.ProbeContext,
		pickFiles:    portalPick,
		thumbs:       map[string]image.Image{},
		loading:      map[string]bool{},
		sem:          make(chan struct{}, 3),
		offlineEvery: 5 * time.Second,
	}
	p.life, p.stopLife = context.WithCancel(context.Background())
	p.content = p.build()
	p.unsub = ctx.Session.Subscribe(p.onChange)
	p.unsubSettings = settings.Subscribe(func(st settings.Settings) {
		kit.Do(func() {
			if !p.closed && !p.importing {
				p.recursive.SetChecked(st.Media.ImportRecursive)
			}
		})
	})
	if ctx.Media != nil {
		p.unsubOff = ctx.Media.OnOfflineChange(func() { kit.Do(p.offlineChanged) })
	}
	p.syncSelection()
	p.refreshPool()
	p.insp.show()
	return p
}

func (p *Page) Content() fyne.CanvasObject { return p.content }

func (p *Page) Activate() {
	p.refreshPool()
	p.files.reload()
	p.insp.show()
	p.checkOffline()
	p.startWatch()
}

func (p *Page) Deactivate() { p.stopWatch() }

func (p *Page) Close() {
	if p.closed {
		return
	}
	p.closed = true
	if pages[p.ctx.Session] == p {
		delete(pages, p.ctx.Session)
	}
	p.stopLife()
	p.stopWatch()
	if p.unsubSettings != nil {
		p.unsubSettings()
		p.unsubSettings = nil
	}
	if p.unsub != nil {
		p.unsub()
		p.unsub = nil
	}
	if p.unsubOff != nil {
		p.unsubOff()
		p.unsubOff = nil
	}
	if p.fileDlg != nil {
		p.fileDlg.dlg.Hide()
		p.fileDlg = nil
	}
}

// missing reports whether the file of media id was not found at the last check.
func (p *Page) missing(id string) bool {
	if p.ctx.Media == nil {
		return false
	}
	m, ok := p.ctx.Session.Project.MediaByID(id)
	return ok && p.ctx.Media.Offline(*m)
}

// checkOffline refreshes the offline map of the project's media in the background.
// Changes arrive through the cache's OnOfflineChange subscription.
func (p *Page) checkOffline() {
	if p.closed || p.checking || p.ctx.Media == nil {
		return
	}
	p.checking = true
	items := slices.Clone(p.ctx.Session.Project.Media)
	cache := p.ctx.Media
	p.spawn(func() {
		cache.CheckOffline(items)
		kit.Do(func() { p.checking = false })
	})
}

// offlineChanged redraws the pool after files went missing or came back.
func (p *Page) offlineChanged() {
	if p.closed {
		return
	}
	for k, img := range p.thumbs { // retry thumbnails that failed while offline
		if img == nil {
			delete(p.thumbs, k)
		}
	}
	p.refreshPool()
	p.insp.reset()
	p.insp.show()
}

// startWatch rechecks missing files every offlineEvery until stopWatch.
func (p *Page) startWatch() {
	if p.watchStop != nil || p.closed || p.offlineEvery <= 0 {
		return
	}
	stop := make(chan struct{})
	p.watchStop = stop
	every := p.offlineEvery
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				kit.Do(func() {
					if p.watchStop == stop {
						p.checkOffline()
					}
				})
			}
		}
	}()
}

func (p *Page) stopWatch() {
	if p.watchStop != nil {
		close(p.watchStop)
		p.watchStop = nil
	}
}

// TypedKey: Delete removes, Enter inserts, arrows move the pool selection.
func (p *Page) TypedKey(k *fyne.KeyEvent) bool {
	switch k.Name {
	case fyne.KeyEscape:
		// clears the pool selection, never the timeline's clips (not visible here)
		p.sel, p.anchor = nil, ""
		p.ctx.Session.SelectMedia("")
		return true
	case fyne.KeyDelete, fyne.KeyBackspace:
		if items := p.selectedItems(); len(items) > 0 {
			p.confirmRemove(items...)
			return true
		}
	case fyne.KeyReturn, fyne.KeyEnter:
		if items := p.selectedItems(); len(items) > 0 {
			p.insert(items, false)
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
	p.recursive = widget.NewCheck("Unterordner einbeziehen", nil)
	p.recursive.Checked = settings.Get().Media.ImportRecursive
	p.progress = widget.NewProgressBar()
	p.progressMsg = canvas.NewText("", studiotheme.Muted)
	p.progressMsg.TextSize = 12
	p.progressBox = container.NewVBox(p.progressMsg, p.progress)
	p.progressBox.Hide()
	if p.poolOnly {
		return p.buildPoolPanel()
	}
	left := kit.Panel("Dateien", nil, container.NewBorder(
		container.NewVBox(p.importBtn, p.folderBtn, p.recursive, spacer(2)),
		p.progressBox, nil, nil, p.files.content()))

	p.filesBox = fixedWidth(minFilesWidth, left)
	p.inspBox = fixedWidth(minInspectorWidth, p.insp.panel())
	p.inner = container.NewHSplit(p.buildPool(nil), p.inspBox)
	p.inner.Offset = defaultInspectorSplit
	p.outer = container.NewHSplit(p.filesBox, p.inner)
	p.outer.Offset = defaultFilesSplit
	return container.New(fynelayout.NewCustomPaddedLayout(4, 4, 4, 4), p.outer)
}

// buildPool builds the pool panel; extra tools (may be nil) go right of the filters.
func (p *Page) buildPool(extra fyne.CanvasObject) fyne.CanvasObject {
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
			c.win = p.ctx.Window
			c.onTap = func(mod fyne.KeyModifier) { p.pick(m.ID, mod) }
			c.onSecondary = func(pos fyne.Position) { p.showMenu(m.ID, pos) }
			c.onDragStart = func() ([]string, string) { return p.dragItems(m.ID) }
			missing := p.ctx.Media != nil && p.ctx.Media.Offline(m)
			c.set(m, p.fps(), p.thumbFor(m), p.isPicked(m.ID), missing)
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
	p.tools = container.NewHBox(fixedWidth(170, container.NewVBox(fynelayout.NewSpacer(), p.count, fynelayout.NewSpacer())), spacer(4), fixedWidth(190, p.search), fixedWidth(118, p.kindSel))
	if extra != nil {
		p.tools.Add(extra)
	}
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
	if c&(session.ChangeProject|session.ChangeSelection) != 0 {
		p.syncSelection()
	}
	if c&session.ChangeProject != 0 {
		p.refreshPool()
	} else if c&session.ChangeSelection != 0 {
		p.updateCount()
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
		p.empty.Hide()
	}
	p.updateCount()
	p.empty.Refresh()
	p.grid.Refresh()
}

// updateCount shows the number of items, filter hits and selected items in the header.
func (p *Page) updateCount() {
	all := len(p.ctx.Session.Project.Media)
	switch {
	case all == 0:
		p.count.Text = ""
	case len(p.view) == all:
		p.count.Text = fmt.Sprintf("%d %s", all, plural(all, "Medium", "Medien"))
	default:
		p.count.Text = fmt.Sprintf("%d von %d", len(p.view), all)
	}
	if n := len(p.sel); n > 1 {
		p.count.Text = fmt.Sprintf("%d ausgewählt · %s", n, p.count.Text)
	}
	p.count.Refresh()
}

// thumbFor returns the pool thumbnail of m, loading it in the background on first use.
func (p *Page) thumbFor(m model.MediaItem) image.Image {
	key := m.ID + "|" + m.Path
	if img, ok := p.thumbs[key]; ok {
		return img
	}
	if p.loading[key] || p.ctx.Media == nil || p.ctx.Media.Offline(m) {
		return nil
	}
	p.loading[key] = true
	cache, life := p.ctx.Media, p.life
	p.spawn(func() {
		select {
		case p.sem <- struct{}{}:
		case <-life.Done():
			return // closed while queued
		}
		if life.Err() != nil {
			<-p.sem
			return
		}
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

// selectedItems returns the selected media in pool (project) order.
func (p *Page) selectedItems() []model.MediaItem {
	var out []model.MediaItem
	for _, m := range p.ctx.Session.Project.Media {
		if p.isPicked(m.ID) {
			out = append(out, m)
		}
	}
	return out
}

func (p *Page) isPicked(id string) bool { return slices.Contains(p.sel, id) }

// syncSelection keeps the pool selection consistent with the project and the session:
// deleted media drop out, and a primary chosen elsewhere (another page, undo) becomes
// the single selection.
func (p *Page) syncSelection() {
	pr := p.ctx.Session.Project
	p.sel = slices.DeleteFunc(p.sel, func(id string) bool {
		_, ok := pr.MediaByID(id)
		return !ok
	})
	primary := p.ctx.Session.SelectedMedia
	switch {
	case primary == "":
		p.sel = nil
	case len(p.sel) == 0 || p.sel[len(p.sel)-1] != primary:
		p.sel = []string{primary}
		p.anchor = primary
	}
}

// pick handles a click on a pool item: plain click selects it alone, Ctrl+click toggles
// it, Shift+click selects the range from the anchor.
func (p *Page) pick(id string, mod fyne.KeyModifier) {
	switch {
	case mod&(fyne.KeyModifierControl|fyne.KeyModifierSuper) != 0:
		if p.isPicked(id) {
			p.sel = slices.DeleteFunc(p.sel, func(s string) bool { return s == id })
		} else {
			p.sel = append(p.sel, id)
			p.anchor = id
		}
	case mod&fyne.KeyModifierShift != 0 && p.anchor != "":
		from, to := -1, -1
		for i, m := range p.view {
			if m.ID == p.anchor {
				from = i
			}
			if m.ID == id {
				to = i
			}
		}
		if from < 0 || to < 0 {
			p.sel, p.anchor = []string{id}, id
			break
		}
		step := 1
		if to < from {
			step = -1
		}
		p.sel = p.sel[:0]
		for i := from; ; i += step {
			p.sel = append(p.sel, p.view[i].ID)
			if i == to {
				break
			}
		}
	default:
		p.sel, p.anchor = []string{id}, id
	}
	primary := ""
	if len(p.sel) > 0 {
		primary = p.sel[len(p.sel)-1]
	}
	p.ctx.Session.SelectMedia(primary)
}

// SelectAll is Ctrl+A on this page: it selects every item the pool shows instead of the
// timeline's clips. It reports true: the key is handled even with an empty pool.
func (p *Page) SelectAll() bool {
	p.selectAll()
	return true
}

// selectAll selects every item the pool shows.
func (p *Page) selectAll() {
	if len(p.view) == 0 {
		return
	}
	p.sel = p.sel[:0]
	for _, m := range p.view {
		p.sel = append(p.sel, m.ID)
	}
	p.anchor = p.sel[0]
	p.ctx.Session.SelectMedia(p.sel[len(p.sel)-1])
}

// dragItems returns what a drag starting on item id carries: the selection when id is
// part of it, otherwise id alone (which becomes the selection).
func (p *Page) dragItems(id string) ([]string, string) {
	if !p.isPicked(id) {
		p.pick(id, 0)
	}
	items := p.selectedItems()
	ids := make([]string, len(items))
	for i, m := range items {
		ids[i] = m.ID
	}
	switch len(items) {
	case 0:
		return nil, ""
	case 1:
		return ids, items[0].Name
	}
	return ids, fmt.Sprintf("%d Clips", len(items))
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
	p.pick(p.view[next].ID, 0)
	p.grid.ScrollTo(next)
	return true
}

func (p *Page) showMenu(id string, pos fyne.Position) {
	if _, ok := p.ctx.Session.Project.MediaByID(id); !ok {
		return
	}
	if !p.isPicked(id) {
		p.pick(id, 0)
	}
	items := p.selectedItems()
	n := len(items)
	what := ""
	if n > 1 {
		what = fmt.Sprintf(" (%d)", n)
	}
	relink := fyne.NewMenuItem("Neu verknüpfen…", func() { p.showRelinkDialog(items[0]) })
	relink.Disabled = n != 1
	menu := fyne.NewMenu("",
		fyne.NewMenuItem("In Timeline einfügen"+what, func() { p.insert(items, false) }),
		fyne.NewMenuItem("Ans Ende anhängen"+what, func() { p.insert(items, true) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Alle auswählen", p.selectAll),
		relink,
		fyne.NewMenuItem("Im Dateimanager zeigen", p.reveal),
		fyne.NewMenuItem("Entfernen…"+what, func() { p.confirmRemove(items...) }),
	)
	widget.ShowPopUpMenuAtPosition(menu, p.ctx.Window.Canvas(), pos)
}

// insert places the items one after another at the playhead (or behind the last clip)
// as one edit, selects the new clips and moves the playhead behind them.
func (p *Page) insert(items []model.MediaItem, atEnd bool) {
	if len(items) == 0 {
		return
	}
	s := p.ctx.Session
	label := "In Timeline einfügen"
	if atEnd {
		label = "Ans Ende anhängen"
	}
	var ids []string
	var end int
	var failed model.MediaItem
	err := s.Edit(label, func(pr *model.Project) error {
		ids = ids[:0]
		end = s.Playhead
		if atEnd {
			end = pr.Duration()
		}
		for _, m := range items {
			id, e, err := placeMedia(pr, m, end, s.SelectedTrack)
			if err != nil {
				failed = m
				return err
			}
			ids, end = append(ids, id), e
		}
		return nil
	})
	if err != nil {
		p.showErr(fmt.Errorf("„%s“ konnte nicht eingefügt werden: %w", failed.Name, err))
		return
	}
	s.SelectClips(ids)
	s.Seek(end)
}

func (p *Page) confirmRemove(items ...model.MediaItem) {
	if len(items) == 0 {
		return
	}
	n := 0
	ids := make([]string, len(items))
	for i, m := range items {
		ids[i] = m.ID
		n += usage(p.ctx.Session.Project, m.ID)
	}
	what := fmt.Sprintf("„%s“ wird", items[0].Name)
	if len(items) > 1 {
		what = fmt.Sprintf("%d Medien werden", len(items))
	}
	msg := fmt.Sprintf("%s aus dem Projekt entfernt. Kein Clip in der Timeline verwendet %s.", what, plural(len(items), "es", "sie"))
	if n > 0 {
		msg = fmt.Sprintf("%s aus dem Projekt entfernt.\n%d %s in der Timeline %s %s und %s ebenfalls gelöscht.",
			what, n, plural(n, "Clip", "Clips"), plural(n, "verwendet", "verwenden"), plural(len(items), "es", "sie"), plural(n, "wird", "werden"))
	}
	title := "Medium entfernen"
	if len(items) > 1 {
		title = "Medien entfernen"
	}
	d := dialog.NewConfirm(title, msg, func(ok bool) {
		if ok {
			p.remove(ids...)
		}
	}, p.ctx.Window)
	d.SetConfirmText("Entfernen")
	d.SetDismissText("Abbrechen")
	d.Show()
}

func (p *Page) remove(ids ...string) {
	label := "Medium entfernen"
	if len(ids) > 1 {
		label = "Medien entfernen"
	}
	p.removeAs(label, ids...)
}

// removeAs removes the media ids as one undo step named label.
func (p *Page) removeAs(label string, ids ...string) {
	err := p.ctx.Session.Edit(label, func(pr *model.Project) error {
		for _, id := range ids {
			if err := pr.RemoveMedia(id); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, model.ErrLocked) {
		p.showErr(errors.New("Medium konnte nicht entfernt werden: Clips davon liegen auf einer gesperrten Spur. Spur entsperren und erneut versuchen"))
		return
	}
	if err != nil {
		p.showErr(fmt.Errorf("Medium konnte nicht entfernt werden: %w", err))
		return
	}
	if p.ctx.Media != nil {
		for _, id := range ids {
			p.ctx.Media.Invalidate(id)
		}
	}
}

// choose asks for paths with the desktop file chooser in the background and falls back
// to the in-app browser when the portal is not available.
func (p *Page) choose(req pickRequest, done func([]string)) {
	if p.picking {
		return
	}
	if req.dir == "" {
		req.dir = p.files.dir
	}
	p.picking = true
	pick := p.pickFiles
	p.spawn(func() {
		paths, err := pick(req)
		kit.Do(func() {
			p.picking = false
			if p.closed {
				return
			}
			if err != nil {
				p.showFileBrowser(req, done)
				return
			}
			if len(paths) > 0 {
				done(paths)
			}
		})
	})
}

func (p *Page) showImportDialog() {
	p.choose(pickRequest{title: "Medien importieren", accept: "Importieren", multi: true}, p.importPaths)
}

func (p *Page) showFolderDialog() {
	p.choose(pickRequest{title: "Ordner importieren", accept: "Importieren", folders: true, multi: true}, p.importPaths)
}

func dialogSize(w fyne.Window) fyne.Size {
	s := w.Canvas().Size()
	return fyne.NewSize(min(900, s.Width*0.9), min(620, s.Height*0.9))
}

// importPaths expands folders (with subfolders when "Unterordner einbeziehen" is on),
// probes the files in the background and adds them to the project in one undo step.
func (p *Page) importPaths(paths []string) {
	if p.importing {
		p.showErr(errors.New("Es läuft bereits ein Import. Nach dessen Abschluss erneut importieren"))
		return
	}
	existing := map[string]bool{}
	for _, m := range p.ctx.Session.Project.Media {
		existing[m.Path] = true
	}
	recursive := p.recursive.Checked
	p.setImporting(true)
	p.progressMsg.Text = "Dateien werden gesucht …"
	p.progressMsg.Refresh()
	p.progress.SetValue(0)
	life := p.life
	p.spawn(func() {
		plan := planImport(paths, existing, recursive)
		if len(plan.files) == 0 {
			kit.Do(func() {
				p.setImporting(false)
				if !p.closed {
					p.nothingToImport(plan)
				}
			})
			return
		}
		kit.Do(func() { p.importProgress(0, len(plan.files)) })
		items := make([]model.MediaItem, len(plan.files))
		errs := make([]error, len(plan.files))
		for i, f := range plan.files {
			if life.Err() != nil {
				return // the page closed: probe nothing more
			}
			items[i], errs[i] = p.probe(life, f)
			done := i + 1
			kit.Do(func() { p.importProgress(done, len(plan.files)) })
		}
		kit.Do(func() { p.finishImport(plan, items, errs) })
	})
}

func (p *Page) nothingToImport(plan importPlan) {
	switch msg := importSummary(plan, nil); {
	case msg != "":
		dialog.ShowInformation("Nichts importiert", msg, p.ctx.Window)
	case plan.duplicates > 0:
		dialog.ShowInformation("Nichts importiert", "Die gewählten Dateien sind bereits im Projekt.", p.ctx.Window)
	default:
		dialog.ShowInformation("Nichts importiert", "Im gewählten Ordner liegen keine unterstützten Mediendateien. Für Dateien in Unterordnern „Unterordner einbeziehen“ einschalten.", p.ctx.Window)
	}
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
	if p.content != nil {
		p.content.Refresh() // re-layout: the progress row appears or goes away
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
		p.sel = []string{ok[0].ID}
		p.anchor = ok[0].ID
		p.ctx.Session.SelectMedia(ok[0].ID)
		for i, m := range p.view {
			if m.ID == ok[0].ID {
				p.grid.ScrollTo(i)
			}
		}
		p.checkOffline()
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
	req := pickRequest{title: fmt.Sprintf("„%s“ neu verknüpfen", m.Name), accept: "Verknüpfen"}
	p.choose(req, func(paths []string) { p.relinkTo(m.ID, paths[0]) })
}

// relinkTo probes path in the background and points media id at it. All cached
// pictures of the item are dropped so every page shows the new file.
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
			p.checkOffline()
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
