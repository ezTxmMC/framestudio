// Package shell is the application frame: project manager, project tabs (multiple open
// projects), page bar, menus, shortcuts, saving and autosave.
//
// OWNER: shell agent. Run is the contract; do not change its signature.
package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	audiopage "framestudio-proto/src/ui/pages/audio"
	colorpage "framestudio-proto/src/ui/pages/color"
	deliverpage "framestudio-proto/src/ui/pages/deliver"
	editpage "framestudio-proto/src/ui/pages/edit"
	mediapage "framestudio-proto/src/ui/pages/media"
)

// autosaveInterval is how often dirty projects with a file are written in the background.
const autosaveInterval = 90 * time.Second

// Run builds the main window for app a, optionally opening the given project files, and blocks.
func Run(a fyne.App, files []string) {
	w := a.NewWindow("Frame Studio")
	w.Resize(fyne.NewSize(1440, 900))
	w.CenterOnScreen()
	sh := New(a, w)
	sh.thumbnails = true
	sh.Start(files)
	stop := sh.StartAutosave(autosaveInterval)
	w.ShowAndRun()
	stop()
	sh.cache.Close() // stops background ffmpeg decoders
}

// pageDef describes a workspace page in page bar order.
type pageDef struct {
	ID, Label string
	Icon      fyne.Resource
	New       func(*page.Context) page.Page
}

func defaultPages() []pageDef {
	return []pageDef{
		{page.Media, "Medien", theme.FolderIcon(), mediapage.New},
		{page.Edit, "Schnitt", theme.MediaVideoIcon(), editpage.New},
		{page.Color, "Farbe", theme.ColorPaletteIcon(), colorpage.New},
		{page.Audio, "Ton", theme.VolumeUpIcon(), audiopage.New},
		{page.Deliver, "Export", theme.UploadIcon(), deliverpage.New},
	}
}

// project is one open project tab with its lazily created pages.
type project struct {
	sess     *session.Session
	ctx      *page.Context
	pages    map[string]page.Page
	contents map[string]fyne.CanvasObject
	current  string // active page ID

	rev       int // bumped on every project change; autosave compares it
	savedAt   time.Time
	autosaved bool
	saveErr   string

	tab      *tabButton
	stopPlay func()
	unsub    func()
}

// Shell owns the main window and all open projects. Use it on the UI thread only.
type Shell struct {
	app      fyne.App
	win      fyne.Window
	cache    *media.Cache
	renderer *render.Renderer
	pageDefs []pageDef

	projects []*project
	active   *project

	root        *fyne.Container
	workspace   fyne.CanvasObject
	content     *fyne.Container
	bars        *bars
	mgr         *manager
	showManager bool

	menu          *menus
	shift         bool
	pendingAnswer func(choice) // open save prompt, for tests

	closeWindow   func()
	drivePlayback func(*session.Session) (stop func())
	// thumbnails enables background rendering of manager card thumbnails.
	thumbnails bool
	now        func() time.Time
}

// New builds the shell UI into w without showing it.
func New(a fyne.App, w fyne.Window) *Shell {
	cache := media.NewCache()
	sh := &Shell{
		app: a, win: w, cache: cache, renderer: render.New(cache),
		pageDefs: defaultPages(), now: time.Now, closeWindow: w.Close,
		drivePlayback: kit.DrivePlayback,
	}
	sh.bars = newBars(sh)
	sh.content = container.NewStack()
	sh.workspace = container.NewBorder(sh.bars.top, sh.bars.bottom, nil, nil, sh.content)
	sh.mgr = newManager(sh)
	sh.root = container.NewStack()
	w.SetPadded(false)
	w.SetContent(sh.root)
	sh.menu = newMenus(sh)
	sh.installKeys()
	w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) { sh.dropped(uris) })
	w.SetCloseIntercept(sh.Quit)
	sh.refreshChrome()
	return sh
}

// Start opens files, or shows the project manager when none could be opened.
func (sh *Shell) Start(files []string) {
	for _, f := range files {
		if err := sh.OpenFile(f); err != nil {
			sh.showErr(err)
		}
	}
	if len(sh.projects) == 0 {
		sh.ShowManager()
	}
}

func (sh *Shell) showErr(err error) {
	if err != nil {
		dialog.ShowError(err, sh.win)
	}
}

// OpenFile opens a project file in a new tab, or focuses its tab if already open.
func (sh *Shell) OpenFile(path string) error {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if p := sh.findOpen(path); p != nil {
		sh.activate(p)
		return nil
	}
	proj, err := store.Load(path)
	if err != nil {
		return err
	}
	p := sh.addProject(proj, path, page.Edit)
	if fi, err := os.Stat(path); err == nil {
		p.savedAt = fi.ModTime()
	}
	store.AddRecent(path)
	sh.activate(p)
	return nil
}

// newProject opens an empty, unsaved project in a new tab.
func (sh *Shell) newProject(name string, t Template) *project {
	proj := model.NewProject(name)
	proj.Settings.Width, proj.Settings.Height, proj.Settings.FPS = t.Width, t.Height, t.FPS
	p := sh.addProject(proj, "", page.Media)
	if v := proj.TrackIndices(model.TrackVideo); len(v) > 0 {
		p.sess.SelectTrack(proj.Tracks[v[0]].ID)
	}
	sh.activate(p)
	return p
}

func (sh *Shell) findOpen(path string) *project {
	for _, p := range sh.projects {
		if p.sess.Path != "" && samePath(p.sess.Path, path) {
			return p
		}
	}
	return nil
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

func (sh *Shell) addProject(proj *model.Project, path, firstPage string) *project {
	s := session.New(proj)
	s.Path = path
	p := &project{sess: s, pages: map[string]page.Page{}, contents: map[string]fyne.CanvasObject{}, current: firstPage}
	p.ctx = &page.Context{
		Window:   sh.win,
		Session:  s,
		Renderer: sh.renderer,
		Media:    sh.cache,
		Navigate: func(id string) { sh.showPage(p, id) },
		ShowErr:  sh.showErr,
	}
	p.tab = newTabButton(func() { sh.activate(p) }, func() { sh.closeProject(p) })
	p.stopPlay = sh.drivePlayback(s)
	p.unsub = s.Subscribe(func(c session.Change) { sh.sessionChanged(p, c) })
	sh.projects = append(sh.projects, p)
	return p
}

func (sh *Shell) sessionChanged(p *project, c session.Change) {
	if c&session.ChangeProject != 0 {
		p.rev++
	}
	if c&(session.ChangeProject|session.ChangeMeta|session.ChangePlayback) != 0 {
		sh.refreshChrome()
	}
}

// Projects returns the open projects' sessions in tab order.
func (sh *Shell) Projects() []*session.Session {
	out := make([]*session.Session, len(sh.projects))
	for i, p := range sh.projects {
		out[i] = p.sess
	}
	return out
}

// Active returns the session of the active tab, or nil.
func (sh *Shell) Active() *session.Session {
	if sh.active == nil {
		return nil
	}
	return sh.active.sess
}

// activate switches to project tab p and leaves the project manager.
func (sh *Shell) activate(p *project) {
	if p == nil {
		return
	}
	// While the manager is shown, the active page is already deactivated.
	visible := !sh.showManager
	switch {
	case sh.active != p:
		if old := sh.active; old != nil {
			if visible {
				sh.deactivatePage(old)
			}
			old.sess.SetPlaying(false)
		}
		sh.active = p
		sh.showPageContent(p)
	case !visible:
		sh.showPageContent(p)
	}
	sh.showWorkspace()
	sh.refreshChrome()
}

// activateIndex switches to the i-th tab.
func (sh *Shell) activateIndex(i int) {
	if i >= 0 && i < len(sh.projects) {
		sh.activate(sh.projects[i])
	}
}

// showPage switches project p to page id; the view only changes when p is active.
func (sh *Shell) showPage(p *project, id string) {
	if p == nil || sh.pageDef(id) == nil {
		return
	}
	if p != sh.active {
		p.current = id
		return
	}
	if sh.showManager {
		p.current = id
		sh.activate(p)
		return
	}
	if p.current == id && p.contents[id] != nil {
		return
	}
	sh.deactivatePage(p)
	p.current = id
	sh.showPageContent(p)
	sh.showWorkspace()
	sh.refreshChrome()
}

// showPageID switches the active project to page id.
func (sh *Shell) showPageID(id string) { sh.showPage(sh.active, id) }

func (sh *Shell) pageDef(id string) *pageDef {
	for i := range sh.pageDefs {
		if sh.pageDefs[i].ID == id {
			return &sh.pageDefs[i]
		}
	}
	return nil
}

// ensurePage creates page id of p on first use.
func (sh *Shell) ensurePage(p *project, id string) page.Page {
	if pg := p.pages[id]; pg != nil {
		return pg
	}
	d := sh.pageDef(id)
	if d == nil {
		return nil
	}
	pg := d.New(p.ctx)
	p.pages[id] = pg
	p.contents[id] = pg.Content()
	return pg
}

func (sh *Shell) showPageContent(p *project) {
	pg := sh.ensurePage(p, p.current)
	if pg == nil {
		return
	}
	sh.content.Objects = []fyne.CanvasObject{p.contents[p.current]}
	sh.content.Refresh()
	pg.Activate()
}

func (sh *Shell) deactivatePage(p *project) {
	if pg := p.pages[p.current]; pg != nil {
		pg.Deactivate()
	}
}

func (sh *Shell) showWorkspace() {
	sh.showManager = false
	sh.root.Objects = []fyne.CanvasObject{sh.workspace}
	sh.root.Refresh()
}

// ShowManager shows the full-window project manager.
func (sh *Shell) ShowManager() {
	if p := sh.active; p != nil && !sh.showManager {
		p.sess.SetPlaying(false)
		sh.deactivatePage(p)
	}
	sh.showManager = true
	sh.mgr.reload()
	sh.root.Objects = []fyne.CanvasObject{sh.mgr.view}
	sh.root.Refresh()
	sh.refreshChrome()
}

// leaveManager returns to the open projects, if any.
func (sh *Shell) leaveManager() {
	sh.activate(sh.active)
}

// closeProject closes tab p, asking to save unsaved changes first.
func (sh *Shell) closeProject(p *project) {
	if p == nil {
		return
	}
	if !p.sess.Dirty {
		sh.closeNow(p)
		return
	}
	sh.activate(p)
	sh.askSave(fmt.Sprintf("„%s“ hat ungespeicherte Änderungen.", p.sess.Project.Name), "Speichern", func(c choice) {
		save, closeIt := resolveClose(p.sess.Dirty, c)
		if save {
			if err := sh.Save(p); err != nil {
				sh.showErr(err)
				return
			}
		}
		if closeIt {
			sh.closeNow(p)
		}
	})
}

func (sh *Shell) closeNow(p *project) {
	i := slices.Index(sh.projects, p)
	if i < 0 {
		return
	}
	wasActive := sh.active == p
	if wasActive && !sh.showManager {
		sh.deactivatePage(p)
	}
	sh.closeNowQuiet(p)
	if wasActive {
		sh.content.Objects = nil
		if n := len(sh.projects); n > 0 {
			sh.activate(sh.projects[min(i, n-1)])
		}
	}
	if len(sh.projects) == 0 {
		sh.ShowManager()
	}
	sh.refreshChrome()
}

// refreshChrome syncs title, tabs, page bar, status and menus with the current state.
func (sh *Shell) refreshChrome() {
	title := "Frame Studio"
	if sh.showManager {
		title += " — Projektmanager"
	} else if p := sh.active; p != nil {
		title += " — " + p.sess.Project.Name
		if p.sess.Dirty {
			title += " •"
		}
	}
	if sh.win.Title() != title {
		sh.win.SetTitle(title)
	}
	sh.bars.refresh()
	sh.menu.refresh()
}
