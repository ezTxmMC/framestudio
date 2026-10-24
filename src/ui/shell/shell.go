// Package shell is the application frame: project manager, project tabs (multiple open
// projects), the custom menu bar, page bar, the command registry with the shell's own
// commands, key routing, settings, the workspace host, saving and autosave.
//
// OWNER: shell agent. Run is the contract; do not change its signature.
package shell

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	wslayout "framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/liveaudio"
	"framestudio-proto/src/ui/page"
	audiopage "framestudio-proto/src/ui/pages/audio"
	colorpage "framestudio-proto/src/ui/pages/color"
	deliverpage "framestudio-proto/src/ui/pages/deliver"
	editpage "framestudio-proto/src/ui/pages/edit"
	mediapage "framestudio-proto/src/ui/pages/media"
	"framestudio-proto/src/ui/settingsui"
	"framestudio-proto/src/ui/winchrome"
	"framestudio-proto/src/ui/workspace"
	"framestudio-proto/src/workgate"
)

// Run builds the main window for app a, optionally opening the given project files, and blocks.
func Run(a fyne.App, files []string) {
	// The settings are normally loaded by main before the theme is chosen; loading again
	// is harmless (every change is saved at once) and reports a damaged file here.
	// When application.Run loaded them already, its load error waits in settingsui.
	loadErr := settingsui.TakeStartupError()
	if !settings.Loaded() {
		_, loadErr = settings.Load()
	}
	w := a.NewWindow("Frame Studio")
	w.Resize(fyne.NewSize(1440, 900))
	w.CenterOnScreen()
	sh := New(a, w)
	sh.thumbnails = true
	// Detached windows go fullscreen on chosen monitors only once the main window is
	// shown and GLFW runs, so the layout is restored when the app has started.
	sh.deferRestore = true
	sh.Start(files)
	sigs := catchSignals(sh)
	a.Lifecycle().SetOnStarted(func() {
		sh.decorateAgain()
		sh.ws.Restore()
		if loadErr != nil {
			sh.showErr(errSettingsLoad(loadErr))
		}
		if msg := settingsui.TakeStartupWarning(); msg != "" {
			sh.showErr(errors.New(msg))
		}
		sh.startDebugHooks()
		sigs.take()
	})
	a.Lifecycle().SetOnExitedForeground(sh.focusLost)
	settingsui.Restart = sh.restart
	defer func() { settingsui.Restart = nil }()
	stop := sh.StartAutosave(autosaveInterval(settings.Get()))
	w.SetMaster() // closing the main window always ends the app
	// Show the window, take the system title bar off at once (before the first frame is
	// drawn, so there is hardly a flicker) and run.
	w.Show()
	sh.decorate()
	a.Run()
	sigs.stop()
	stop()
	if !sh.shutdownDone {
		// The run loop ended without Quit (a signal before the handler was in place, or
		// the session ending): remember the projects and the layout anyway. GLFW is gone,
		// so the workspace saves what it knows without touching the windows.
		sh.saveLastOpen()
		sh.ws.ShutdownAfterLoop()
	}
	// Quit normally released everything already (exports and previews, then audio); if
	// the run loop ended another way, do it now, then stop the decoders shared by all.
	sh.releaseAll()
	sh.dispose()
	sh.cache.Close()
}

// signalCatcher turns SIGINT and SIGTERM (Strg+C in a terminal, kill, logging out) into
// a normal shutdown on the UI thread: projects and layout are remembered, detached
// windows closed. Fyne's own handler would end the run loop at once, skipping that.
type signalCatcher struct {
	ch    chan os.Signal
	quit  chan struct{}
	done  chan struct{}
	timer *time.Timer
}

func catchSignals(sh *Shell) *signalCatcher {
	c := &signalCatcher{ch: make(chan os.Signal, 1), quit: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		select {
		case <-c.ch:
			fyne.Do(func() {
				if !sh.shutdownDone {
					sh.shutdown()
				}
			})
		case <-c.quit:
		}
	}()
	return c
}

// take replaces Fyne's signal handler (installed when the run loop starts) by c; once
// more a moment later in case Fyne's goroutine registered late.
func (c *signalCatcher) take() {
	grab := func() {
		signal.Reset(os.Interrupt, syscall.SIGTERM)
		signal.Notify(c.ch, os.Interrupt, syscall.SIGTERM)
	}
	grab()
	c.timer = time.AfterFunc(time.Second, grab)
}

// stop ends the handler after the run loop.
func (c *signalCatcher) stop() {
	if c.timer != nil {
		c.timer.Stop()
	}
	signal.Stop(c.ch)
	close(c.quit)
	<-c.done
}

// newWorkspace creates the workspace manager; tests swap in a fake display backend.
var newWorkspace = workspace.New

// Close releases what Run releases after the run loop: open projects, subscriptions and
// the shared media cache. For hosts that build the shell with New (tests, tools).
func (sh *Shell) Close() {
	sh.releaseAll()
	sh.dispose()
	sh.cache.Close()
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
	autosaves []*autosaveJob // recent autosaves, to undo them on "Verwerfen"

	// cmds holds the commands of the created pages that provide some (page.CommandProvider).
	cmds map[string][]command.Binding

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
	saves    *saveLog

	projects []*project
	active   *project

	npd *newProject        // the open "Neues Projekt" dialog (tests drive it)
	psd *projectSettingsUI // the open "Projekteinstellungen" dialog

	chromeOn         bool              // the main window shows the custom title bar (chrome.go)
	retryDecorate    bool              // decorate failed once: ask again after the start
	undecorated      bool              // the system title bar was taken off the native window
	chrome           *winchrome.Chrome // title bar and frame while chromeOn
	unwatch          func()            // stops the window state watch
	root             *fyne.Container
	frame            *fyne.Container // menu bar, tabs, page content and page bar
	content          *fyne.Container
	workspaceBody    *fyne.Container
	mainDockSplit    *container.Split
	mainDockVertical bool
	mainDockContent  fyne.CanvasObject
	dockedPanels     []string
	bars             *bars
	mgr              *manager
	toast            *toaster
	showManager      bool

	// Commands, keys and menus (commands.go, keys.go).
	reg            *command.Registry
	menuBar        menuOpener
	menuOpened     []int // menus opened by keys, for tests
	shutdownDone   bool  // shutdown ran (Run saves projects and layout itself otherwise)
	undoLabel      string
	redoLabel      string
	boundFor       *project        // project whose page commands are bound
	bound          map[string]bool // page IDs bound for boundFor
	activeScope    string
	dispatchingKey bool // a key (not the menu) is running a command
	keySig         string
	registered     []fyne.Shortcut
	deferRestore   bool // Run restores the workspace once the app has started
	restarting     bool // quitting for a restart (settingsui.Restart)

	// Workspace host (host.go) and settings (prefs.go).
	ws           *workspace.Manager
	activeFns    []func()
	notified     *project
	pageLayouts  map[string]wslayout.Page
	autosaveSet  chan time.Duration
	disposers    []func()
	openPath     func(string) error // tests replace xdg-open
	lastSettings settings.Settings

	modifiers     func() fyne.KeyModifier         // modifiers held with the current key event
	shortcuts     map[string]func(fyne.Shortcut)  // window shortcuts by ShortcutName, for tests
	pendingAnswer func(choice)                    // open save prompt, for tests
	confirmHook   func(message string, ok func()) // tests answer busy prompts

	closeWindow   func()
	drivePlayback func(*session.Session) (stop func())
	// thumbnails enables background rendering of manager card thumbnails.
	thumbnails bool
	now        func() time.Time
}

// New builds the shell UI into w without showing it.
func New(a fyne.App, w fyne.Window) *Shell {
	cache := media.NewCache()
	startPerfServices(cache)
	sh := &Shell{
		app: a, win: w, cache: cache, renderer: render.New(cache),
		pageDefs: defaultPages(), now: time.Now, closeWindow: w.Close, modifiers: heldModifiers,
		saves: newSaveLog(), reg: command.NewRegistry(), bound: map[string]bool{},
		pageLayouts: map[string]wslayout.Page{}, autosaveSet: make(chan time.Duration, 1),
	}
	sh.lastSettings = settings.Get()
	store.SetProjectsDir(sh.lastSettings.General.ProjectsDir)
	sh.reg.SetKeymap(sh.lastSettings.Keymap)
	sh.drivePlayback = func(s *session.Session) func() {
		stopClock := kit.DrivePlayback(s)
		stopSound := liveaudio.Drive(s, sh.showErr)
		unwatch := liveaudio.WatchMedia(s, sh.cache) // files coming back online sound at once
		stopGate := followWorkGate(s)
		return func() {
			stopGate()
			unwatch()
			stopClock()
			stopSound()
		}
	}
	sh.bindGlobal()
	sh.ws = newWorkspace(sh)
	sh.reg.Bind(command.Global, sh.ws.Bindings()...)

	sh.toast = newToaster()
	sh.bars = newBars(sh)
	sh.content = container.NewStack()
	sh.workspaceBody = container.NewStack(sh.content)
	sh.frame = container.NewBorder(sh.bars.top, sh.bars.bottom, nil, nil, sh.workspaceBody)
	sh.mgr = newManager(sh)
	sh.root = container.NewStack()
	w.SetPadded(false)
	// The menu row (the title bar in the custom chrome) stays on top of the workspace
	// and the project manager alike. The native title bar goes in decorate, once the
	// window exists.
	sh.chromeOn = winchrome.Wanted(sh.lastSettings.Display)
	sh.installContent()
	winchrome.OnClose(w, sh.Quit)
	winchrome.OnFullscreen(w, sh.setFullscreen)
	sh.installKeys()
	sh.disposers = append(sh.disposers,
		sh.reg.Subscribe(sh.registerShortcuts),
		settings.Subscribe(sh.settingsChanged), sh.releaseChrome)
	w.SetOnDropped(sh.droppedAt)
	w.SetCloseIntercept(sh.Quit)
	sh.refreshChrome()
	return sh
}

// Commands returns the app's command registry.
func (sh *Shell) Commands() *command.Registry { return sh.reg }

// Workspace returns the workspace manager (detached windows, layouts).
func (sh *Shell) Workspace() *workspace.Manager { return sh.ws }

// dispose ends the subscriptions of the shell (settings, registry). Safe to call twice.
func (sh *Shell) dispose() {
	for _, f := range sh.disposers {
		f()
	}
	sh.disposers = nil
}

// Start opens files; without files it follows the startup setting ("manager", "last"
// reopens the projects open at the last quit, "new" creates a project from the default
// template). It shows the project manager when no project is open, then restores the
// workspace layout.
func (sh *Shell) Start(files []string) {
	for _, f := range files {
		if err := sh.OpenFile(f); err != nil {
			sh.showErr(err)
		}
	}
	if len(files) == 0 {
		g := settings.Get().General
		switch g.Startup {
		case "last":
			sh.reopen(g.LastOpen)
		case "new":
			sh.newProject(defaultProjectName(), settings.Get().TemplateOrDefault(g.DefaultTemplate))
		}
	}
	if len(sh.projects) == 0 {
		sh.ShowManager()
	}
	if !sh.deferRestore {
		sh.ws.Restore()
	}
}

// reopen opens the projects of the last session; missing files are reported once.
func (sh *Shell) reopen(paths []string) {
	var missing []string
	for _, f := range paths {
		if _, err := os.Stat(f); err != nil {
			missing = append(missing, filepath.Base(f))
			continue
		}
		sh.showErr(sh.OpenFile(f))
	}
	if len(missing) > 0 {
		sh.showErr(errMissingLast(missing))
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
	proj.Settings.DropFrame = t.DropFrame && t.FPS.CanDropFrame()
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
	// The loop is not saved with a project: every tab starts with the setting.
	s.Loop = settings.Get().Playback.LoopDefault
	p := &project{sess: s, pages: map[string]page.Page{}, contents: map[string]fyne.CanvasObject{}, current: firstPage,
		cmds: map[string][]command.Binding{}}
	p.ctx = &page.Context{
		Window:   sh.win,
		Session:  s,
		Renderer: sh.renderer,
		Media:    sh.cache,
		Navigate: func(id string) { sh.showPage(p, id) },
		ShowErr:  sh.showErr,
		Commands: sh.reg,
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
			sh.captureLayouts(old)
		}
		sh.applyLayouts(p) // the page layout is global: p shows what the user arranged
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
	if sh.ws != nil {
		sh.ws.SwitchWorkspace(id)
	}
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
	if docked, ok := pg.(interface{ SetDockedPanels([]string) }); ok {
		docked.SetDockedPanels(sh.dockedPanels)
	}
	if t, ok := pg.(toastUser); ok {
		t.SetToast(sh.notify)
	}
	if la, ok := pg.(page.LayoutAware); ok {
		if sh.active != nil && sh.active != p {
			sh.captureLayouts(sh.active) // what the user arranged in the shown tab
		}
		if l, ok := sh.pageLayouts[id]; ok {
			la.ApplyLayout(l)
		}
	}
	if cp, ok := pg.(page.CommandProvider); ok {
		p.cmds[id] = cp.Commands()
		if sh.boundFor == p {
			sh.reg.Bind(command.PageScope(id), p.cmds[id]...)
			sh.bound[id] = true
		}
	}
	return pg
}

// notify shows a toast from a page (finished exports). Successes respect the
// Export.Notify setting; failures always show.
func (sh *Shell) notify(title, message string, failed bool) {
	if !failed && !settings.Get().Export.Notify {
		return
	}
	sh.toast.show(title, message, failed)
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
	sh.mgr.cancelThumbnails()
	sh.showManager = false
	sh.root.Objects = []fyne.CanvasObject{sh.frame, sh.toast.layer}
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
	sh.root.Objects = []fyne.CanvasObject{sh.mgr.view, sh.toast.layer}
	sh.root.Refresh()
	sh.refreshChrome()
}

// leaveManager returns to the open projects, if any.
func (sh *Shell) leaveManager() {
	sh.activate(sh.active)
}

// closeProject closes tab p, asking first if an export would be cancelled or changes
// are unsaved.
func (sh *Shell) closeProject(p *project) { sh.closeProjectThen(p, nil) }

// closeProjectThen is closeProject; then runs after p closed (not when cancelled).
func (sh *Shell) closeProjectThen(p *project, then func()) {
	if p == nil {
		return
	}
	if busy := projectBusy(p); busy != "" {
		sh.activate(p)
		sh.confirmBusy(busy+". Abbrechen und schließen?", "Abbrechen und schließen", func() { sh.closeSaved(p, then) })
		return
	}
	sh.closeSaved(p, then)
}

// closeSaved closes p, asking to save unsaved changes first. Without the ConfirmClose
// setting, projects that have a file are saved without asking.
func (sh *Shell) closeSaved(p *project, then func()) {
	closeNow := func() {
		sh.closeNow(p)
		if then != nil {
			then()
		}
	}
	if !p.sess.Dirty {
		closeNow()
		return
	}
	if !settings.Get().General.ConfirmClose && p.sess.Path != "" {
		if err := sh.Save(p); err != nil {
			sh.showErr(err)
			return
		}
		closeNow()
		return
	}
	sh.activate(p)
	mark := sh.saves.next()
	sh.askSave(fmt.Sprintf("„%s“ hat ungespeicherte Änderungen.", p.sess.Project.Name), "Speichern", func(c choice) {
		if c == choiceDiscard {
			sh.discardAutosaves(p, mark)
		}
		save, closeIt := resolveClose(p.sess.Dirty || c == choiceDiscard, c)
		if save {
			if err := sh.Save(p); err != nil {
				sh.showErr(err)
				return
			}
		}
		if closeIt {
			closeNow()
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

// projectBusy describes background work of p that closing would cancel, or "".
func projectBusy(p *project) string {
	var parts []string
	for _, id := range slices.Sorted(maps.Keys(p.pages)) {
		if b, ok := p.pages[id].(page.Busy); ok {
			if msg := b.Busy(); msg != "" {
				parts = append(parts, msg)
			}
		}
	}
	return strings.Join(parts, "; ")
}

// confirmBusy asks before cancelling background work; ok runs on confirmation.
func (sh *Shell) confirmBusy(message, confirm string, ok func()) {
	if sh.confirmHook != nil {
		sh.confirmHook(message, ok)
		return
	}
	d := dialog.NewConfirm("Export läuft", message, func(yes bool) {
		if yes {
			ok()
		}
	}, sh.win)
	d.SetConfirmText(confirm)
	d.SetDismissText("Weiterlaufen lassen")
	d.Show()
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
	sh.syncCommands()
	sh.notifyActive()
	sh.reg.Refresh() // menus update enabled states and check marks
}

// followWorkGate reports the session's play state to the work gate, so thumbnails,
// waveforms, proxies and other background work pause while the user plays (playback
// has priority). The playhead ticks keep the gate's watchdog alive.
func followWorkGate(s *session.Session) func() {
	key := s
	unsub := s.Subscribe(func(c session.Change) {
		if c&session.ChangePlayback != 0 {
			workgate.SetPlaying(key, s.Playing)
		}
		if c&session.ChangePlayhead != 0 && s.Playing {
			workgate.ClockTick()
		}
	})
	return func() {
		unsub()
		workgate.SetPlaying(key, false)
	}
}
