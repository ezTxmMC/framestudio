package shell

import (
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/keymapui"
	"framestudio-proto/src/ui/menubar"
	"framestudio-proto/src/ui/page"
	deliverpage "framestudio-proto/src/ui/pages/deliver"
	mediapage "framestudio-proto/src/ui/pages/media"
	"framestudio-proto/src/ui/settingsui"
	"framestudio-proto/src/ui/winchrome"
)

// Page scopes. The registry holds one binding set per page ID (command.PageScope); the
// shell binds the pages of the ACTIVE project tab only and rebinds them when the tab
// changes (syncScopes). Exactly the scope of the visible page is active; none while the
// project manager is shown.

// bindGlobal registers every command the shell owns (see the OWNER comments in
// command/ids.go) and the dynamic submenus of recent and open projects.
func (sh *Shell) bindGlobal() {
	reg := sh.reg
	open := func() bool { return sh.workspaceProject() != nil }
	hasProject := func() bool { return sh.active != nil }
	withSess := func(fn func(s *session.Session)) func() {
		return func() {
			if p := sh.workspaceProject(); p != nil {
				fn(p.sess)
			}
		}
	}
	b := func(id string, run func(), enabled func() bool) command.Binding {
		return command.Binding{ID: id, Run: run, Enabled: enabled}
	}
	reg.Bind(command.Global,
		command.Binding{ID: "file.new", Run: sh.newProjectDialog, Icon: theme.ContentAddIcon()},
		command.Binding{ID: "file.open", Run: sh.openDialog, Icon: theme.FolderOpenIcon()},
		command.Binding{ID: "file.manager", Run: sh.ShowManager, Icon: theme.HomeIcon()},
		command.Binding{ID: "file.save", Run: sh.saveActive, Enabled: hasProject, Icon: theme.DocumentSaveIcon()},
		b("file.saveAs", sh.saveAs, hasProject),
		b("file.saveAll", sh.saveAll, func() bool { return len(dirtyProjects(sh.projects)) > 0 }),
		b("file.revert", sh.revertActive, func() bool {
			p := sh.active
			return p != nil && p.sess.Dirty && p.sess.Path != ""
		}),
		b("file.projectSettings", sh.projectSettings, hasProject),
		b("file.close", sh.closeActive, open),
		b("file.closeAll", sh.closeAll, func() bool { return len(sh.projects) > 0 }),
		b("file.quit", sh.Quit, nil),

		sh.undoBinding(), sh.redoBinding(),
		b("app.settings", func() { settingsui.Show(sh.app, sh.win, sh.reg, "") }, nil),
		b("app.keymap", func() { keymapui.Show(sh.win, sh.reg) }, nil),
		b("app.palette", func() { menubar.ShowPalette(sh.win, sh.reg) }, nil),
	)

	// Transport: every page with the active project. Keys only reach these on pages
	// with a viewer (see viewer); the menu works everywhere.
	viewer := sh.viewerEnabled
	reg.Bind(command.Global,
		b("play.toggle", withSess(func(s *session.Session) { s.TogglePlaying() }), viewer),
		b("play.reverse", withSess(func(s *session.Session) { s.Shuttle(-1) }), viewer),
		b("play.stop", withSess(func(s *session.Session) { s.Shuttle(0) }), viewer),
		b("play.forward", withSess(func(s *session.Session) { s.Shuttle(+1) }), viewer),
		b("play.start", withSess(func(s *session.Session) { s.Seek(0) }), viewer),
		b("play.end", withSess(func(s *session.Session) { s.Seek(s.Project.Duration()) }), viewer),
		b("play.frameBack", withSess(func(s *session.Session) { s.Seek(s.Playhead - 1) }), viewer),
		b("play.frameForward", withSess(func(s *session.Session) { s.Seek(s.Playhead + 1) }), viewer),
		b("play.secondBack", withSess(func(s *session.Session) { s.Seek(s.Playhead - s.FPS()) }), viewer),
		b("play.secondForward", withSess(func(s *session.Session) { s.Seek(s.Playhead + s.FPS()) }), viewer),
		b("play.prevEdit", withSess(func(s *session.Session) { s.Seek(s.PrevEdit()) }), viewer),
		b("play.nextEdit", withSess(func(s *session.Session) { s.Seek(s.NextEdit()) }), viewer),
		b("play.gotoIn", withSess(func(s *session.Session) { s.Seek(s.In) }), sh.rangeEnabled(false)),
		b("play.gotoOut", withSess(func(s *session.Session) { s.Seek(s.Out) }), sh.rangeEnabled(true)),
		b("play.inOut", withSess(playRange), sh.rangeEnabled(true)),
		command.Binding{ID: "play.loop", Run: sh.toggleLoop, Enabled: open, Checked: func() bool {
			p := sh.workspaceProject()
			return p != nil && p.sess.Loop
		}},
		// Fallbacks of edit-page commands for the other viewer pages (Farbe, Ton,
		// Export); the edit page's own bindings win while it is visible.
		b("edit.selectAll", sh.selectAll, open),
		b("edit.deselect", withSess(func(s *session.Session) { s.SelectClip("") }), viewer),
		b("timeline.setIn", withSess(setIn), viewer),
		b("timeline.setOut", withSess(setOut), viewer),
		b("timeline.clearRange", withSess(func(s *session.Session) { s.ClearRange() }), func() bool {
			p := sh.workspaceProject() // a lone In or Out point can be cleared too
			return viewer() && (p.sess.In > 0 || p.sess.Out > 0)
		}),
	)

	// Settings toggles.
	for _, q := range []string{"auto", "full", "half", "quarter"} {
		reg.Bind(command.Global, sh.radio("play.quality."+q,
			func(s *settings.Settings) *string { return &s.Playback.PreviewQuality }, q))
	}
	for _, f := range []string{"page", "smooth", "off"} {
		reg.Bind(command.Global, sh.radio("timeline.follow."+f,
			func(s *settings.Settings) *string { return &s.Playback.Follow }, f))
	}
	reg.Bind(command.Global,
		sh.toggle("play.scrubAudio", func(s *settings.Settings) *bool { return &s.Audio.ScrubAudio }),
		sh.toggle("play.mute", func(s *settings.Settings) *bool { return &s.Audio.Muted }),
		sh.toggle("timeline.showFilmstrip", func(s *settings.Settings) *bool { return &s.Timeline.ShowFilmstrip }),
		sh.toggle("timeline.showWaveforms", func(s *settings.Settings) *bool { return &s.Timeline.ShowWaveforms }),
		sh.toggle("view.safeArea", func(s *settings.Settings) *bool { return &s.Playback.SafeArea }),
		sh.toggle("view.pageBar", func(s *settings.Settings) *bool { return &s.Display.PageBar }),
		sh.toggle("view.statusBar", func(s *settings.Settings) *bool { return &s.Display.StatusBar }),
	)

	// Pages, window, help.
	for _, d := range sh.pageDefs {
		id := d.ID
		reg.Bind(command.Global, command.Binding{ID: "view.page." + id, Icon: d.Icon,
			Run:     func() { sh.showPage(sh.active, id) },
			Enabled: hasProject,
			Checked: func() bool { p := sh.workspaceProject(); return p != nil && p.current == id },
		})
	}
	reg.Bind(command.Global,
		command.Binding{ID: "view.fullscreen", Run: sh.toggleFullscreen, Checked: func() bool { return sh.win.FullScreen() }},
		b("window.minimize", func() { winchrome.Sys.Minimize(sh.win) }, nil),
		command.Binding{ID: "window.zoom", Run: func() { winchrome.Sys.ToggleMaximize(sh.win) },
			MenuLabel: func() string {
				if winchrome.Sys.State(sh.win).Maximized || sh.win.FullScreen() {
					return "Wiederherstellen"
				}
				return "Maximieren"
			}},
		b("window.close", sh.closeFocusedWindow, nil),
		sh.customChromeBinding(),
		sh.buttonsBinding("window.buttons.mac", "mac"),
		sh.buttonsBinding("window.buttons.win", "win"),
		b("window.nextProject", func() { sh.cycleProject(+1) }, func() bool { return len(sh.projects) > 1 }),
		b("window.prevProject", func() { sh.cycleProject(-1) }, func() bool { return len(sh.projects) > 1 }),
		command.Binding{ID: "help.shortcuts", Run: sh.showShortcuts, Icon: theme.HelpIcon()},
		b("help.diagnostics", sh.showDiagnostics, nil),
		b("help.configFolder", sh.openConfigFolder, nil),
		command.Binding{ID: "help.about", Run: sh.showAbout, Icon: theme.InfoIcon()},
	)

	reg.SetDynamic(command.DynRecent, sh.recentItems)
	reg.SetDynamic(command.DynProjects, sh.projectItems)
}

// viewerEnabled enables transport and range commands while a project is shown. While a
// key is being dispatched on Medien (no viewer, no timeline) they stay off, so Space or
// I do not act on a timeline the user cannot see; the menu still works there.
func (sh *Shell) viewerEnabled() bool {
	p := sh.workspaceProject()
	return p != nil && !(sh.dispatchingKey && p.current == page.Media)
}

// rangeEnabled is viewerEnabled plus an In point (or a complete range when full).
func (sh *Shell) rangeEnabled(full bool) func() bool {
	return func() bool {
		if !sh.viewerEnabled() {
			return false
		}
		s := sh.workspaceProject().sess
		if full {
			return s.HasRange()
		}
		return s.In > 0 || s.HasRange()
	}
}

func setIn(s *session.Session) {
	if s.Out <= s.Playhead {
		s.Out = 0
	}
	s.SetIn(s.Playhead)
}

func setOut(s *session.Session) {
	if s.Playhead <= s.In {
		s.In = 0
	}
	s.SetOut(s.Playhead)
}

// playRange plays from In to Out once (the session stops at Out unless looping).
func playRange(s *session.Session) {
	if !s.HasRange() {
		return
	}
	s.SetPlaying(false)
	s.Seek(s.In)
	s.SetPlaying(true)
}

// toggle binds a command that flips a boolean setting.
func (sh *Shell) toggle(id string, field func(*settings.Settings) *bool) command.Binding {
	return command.Binding{ID: id,
		Run: func() {
			sh.updateSettings(func(s *settings.Settings) { *field(s) = !*field(s) })
		},
		Checked: func() bool { s := settings.Get(); return *field(&s) },
	}
}

// customChromeBinding is the toggle for the own title bar; it shows checked only while the
// custom chrome is really active (the window system can do it), not just wanted.
func (sh *Shell) customChromeBinding() command.Binding {
	b := sh.toggle("window.customChrome", func(s *settings.Settings) *bool { return &s.Display.CustomChrome })
	b.Checked = func() bool { return winchrome.Wanted(settings.Get().Display) }
	b.Run = func() {
		sh.updateSettings(func(s *settings.Settings) { s.Display.CustomChrome = !s.Display.CustomChrome })
	}
	return b
}

// buttonsBinding picks the window button style; it has no effect (and is disabled) while
// the own title bar is inactive.
func (sh *Shell) buttonsBinding(id, value string) command.Binding {
	b := sh.radio(id, func(s *settings.Settings) *string { return &s.Display.WindowButtons }, value)
	b.Enabled = func() bool { return winchrome.Wanted(settings.Get().Display) }
	return b
}

// radio binds one choice of a string setting; Checked marks the current one.
func (sh *Shell) radio(id string, field func(*settings.Settings) *string, value string) command.Binding {
	return command.Binding{ID: id,
		Run:     func() { sh.updateSettings(func(s *settings.Settings) { *field(s) = value }) },
		Checked: func() bool { s := settings.Get(); return *field(&s) == value },
	}
}

func (sh *Shell) updateSettings(fn func(*settings.Settings)) {
	if err := settings.Update(fn); err != nil {
		sh.showErr(errSettingsSave(err))
	}
}

// undoBinding is edit.undo; menus show the step it undoes ("Rückgängig: Clip teilen"),
// help, palette and keymap editor the fixed catalog label "Rückgängig".
func (sh *Shell) undoBinding() command.Binding {
	return command.Binding{ID: "edit.undo", Run: sh.undo, Icon: theme.ContentUndoIcon(),
		MenuLabel: func() string { return sh.undoMenuLabel() },
		Enabled:   func() bool { p := sh.workspaceProject(); return p != nil && p.sess.CanUndo() }}
}

func (sh *Shell) redoBinding() command.Binding {
	return command.Binding{ID: "edit.redo", Run: sh.redo, Icon: theme.ContentRedoIcon(),
		MenuLabel: func() string { return sh.redoMenuLabel() },
		Enabled:   func() bool { p := sh.workspaceProject(); return p != nil && p.sess.CanRedo() }}
}

func (sh *Shell) undoMenuLabel() string {
	if p := sh.workspaceProject(); p != nil && p.sess.UndoLabel() != "" {
		return "Rückgängig: " + p.sess.UndoLabel()
	}
	return "Rückgängig"
}

func (sh *Shell) redoMenuLabel() string {
	if p := sh.workspaceProject(); p != nil && p.sess.RedoLabel() != "" {
		return "Wiederholen: " + p.sess.RedoLabel()
	}
	return "Wiederholen"
}

// syncCommands brings undo labels and page scopes in line with the shown project.
func (sh *Shell) syncCommands() {
	if sh.reg == nil {
		return
	}
	if u, r := sh.undoMenuLabel(), sh.redoMenuLabel(); u != sh.undoLabel || r != sh.redoLabel {
		sh.undoLabel, sh.redoLabel = u, r
		sh.reg.Refresh() // open menus show the new step names
	}
	sh.syncScopes()
}

// syncScopes binds the commands of the active project's pages and activates the scope of
// the visible page. Page commands are rebound when the active tab changes, so one scope
// per page ID serves all open projects.
func (sh *Shell) syncScopes() {
	p := sh.active
	if p != sh.boundFor {
		for id := range sh.bound {
			sh.reg.Unbind(command.PageScope(id))
		}
		sh.bound = map[string]bool{}
		sh.boundFor = p
		sh.bindProjectScope(p)
	}
	if p != nil {
		for _, id := range slices.Sorted(maps.Keys(p.cmds)) {
			if !sh.bound[id] {
				sh.reg.Bind(command.PageScope(id), p.cmds[id]...)
				sh.bound[id] = true
			}
		}
	}
	want := ""
	if p != nil && !sh.showManager {
		want = command.PageScope(p.current)
	}
	if want != sh.activeScope {
		if sh.activeScope != "" {
			sh.reg.SetScopeActive(sh.activeScope, false)
		}
		if want != "" {
			sh.reg.SetScopeActive(want, true)
		}
		sh.activeScope = want
	}
}

// projectScope holds the commands of the active project that work from every page
// (import, export, render queue); they open their page when needed. Always active;
// rebound on every tab change. A visible page's own bindings of the same IDs win.
const projectScope = "project"

// bindProjectScope binds the page-independent commands of p (none when p is nil).
func (sh *Shell) bindProjectScope(p *project) {
	sh.reg.Unbind(projectScope)
	if p == nil {
		return
	}
	sh.reg.SetScopeActive(projectScope, true)
	sh.reg.Bind(projectScope, mediapage.GlobalCommands(p.ctx)...)
	sh.reg.Bind(projectScope, deliverpage.GlobalCommands(p.ctx)...)
}

// unbindProject drops the page commands of p when its tab closes.
func (sh *Shell) unbindProject(p *project) {
	if sh.boundFor != p {
		return
	}
	for id := range sh.bound {
		sh.reg.Unbind(command.PageScope(id))
	}
	sh.reg.Unbind(projectScope)
	sh.bound = map[string]bool{}
	sh.boundFor = nil
}

// saveAll saves every project with unsaved changes (new ones into the library).
func (sh *Shell) saveAll() {
	for _, p := range dirtyProjects(sh.projects) {
		if err := sh.Save(p); err != nil {
			sh.showErr(err)
			return
		}
	}
}

// revertActive reloads the active project from its file after asking.
func (sh *Shell) revertActive() {
	p := sh.active
	if p == nil || p.sess.Path == "" {
		return
	}
	d := dialog.NewConfirm("Gespeicherte Version wiederherstellen?",
		"Alle Änderungen seit dem letzten Speichern an „"+p.sess.Project.Name+"“ gehen verloren.",
		func(ok bool) {
			if ok {
				sh.showErr(sh.revert(p))
			}
		}, sh.win)
	d.SetConfirmText("Wiederherstellen")
	d.SetDismissText("Abbrechen")
	d.Show()
}

// revert replaces p by the version on disk in the same tab position and page.
func (sh *Shell) revert(p *project) error {
	path := p.sess.Path
	proj, err := store.Load(path)
	if err != nil {
		return err
	}
	i := slices.Index(sh.projects, p)
	if i < 0 {
		return nil
	}
	current := p.current
	if sh.active == p && !sh.showManager {
		sh.deactivatePage(p)
	}
	sh.unbindProject(p)
	sh.closePages(p)
	p.stopPlay()
	p.unsub()
	sh.projects = slices.Delete(sh.projects, i, i+1)
	if sh.active == p {
		sh.active = nil
	}
	q := sh.addProject(proj, path, current)
	// addProject appends; move the tab back to where it was.
	sh.projects = slices.Insert(slices.DeleteFunc(sh.projects, func(x *project) bool { return x == q }), i, q)
	q.savedAt = p.savedAt
	sh.activate(q)
	return nil
}

// closeAll closes every project tab, asking for each with unsaved changes; cancelling
// a prompt stops there.
func (sh *Shell) closeAll() {
	queue := slices.Clone(sh.projects)
	var next func()
	next = func() {
		for len(queue) > 0 {
			p := queue[0]
			queue = queue[1:]
			if slices.Contains(sh.projects, p) {
				sh.closeProjectThen(p, next)
				return
			}
		}
	}
	next()
}

// projectSettings shows the project settings: the inspector of the edit page shows them
// while no clip is selected.
func (sh *Shell) projectSettings() {
	p := sh.active
	if p == nil {
		return
	}
	p.sess.SelectClip("")
	sh.showPage(p, page.Edit)
	sh.toast.show("Projekteinstellungen", "Name, Auflösung und Bildrate im Inspector rechts ändern.", false)
}

func (sh *Shell) cycleProject(dir int) {
	n := len(sh.projects)
	if n == 0 {
		return
	}
	i := max(slices.Index(sh.projects, sh.active), 0)
	sh.activate(sh.projects[((i+dir)%n+n)%n])
}

// toggleFullscreen puts the main window fullscreen on the monitor it is on, or back. The
// state comes from the window itself: a restored layout or the monitor dialog may have
// made it fullscreen.
func (sh *Shell) toggleFullscreen() { sh.setFullscreen(!sh.win.FullScreen()) }

func (sh *Shell) openConfigFolder() {
	dir, err := settings.Dir()
	if err != nil {
		sh.showErr(errConfigDir(err))
		return
	}
	sh.showErr(sh.openFolder(dir))
}

// openFolder shows dir in the file manager (xdg-open); tests replace it.
func (sh *Shell) openFolder(dir string) error {
	if sh.openPath != nil {
		return sh.openPath(dir)
	}
	cmd := exec.Command("xdg-open", dir)
	if err := cmd.Start(); err != nil {
		return errOpenFolder(dir, err)
	}
	go func() { _ = cmd.Wait() }() // reap the helper
	return nil
}

// recentItems lists recently opened projects for Datei › Zuletzt geöffnet.
func (sh *Shell) recentItems() []command.Item {
	var items []command.Item
	for _, path := range store.Recent() {
		p := path
		label := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		if o := sh.findOpen(p); o != nil {
			label = o.sess.Project.Name
		}
		items = append(items, command.Item{Label: label + "  —  " + shortDir(filepath.Dir(p)), Run: func() {
			sh.showErr(sh.OpenFile(p))
		}})
		if len(items) == 10 {
			break
		}
	}
	if len(items) == 0 {
		items = append(items, command.Item{Label: "Noch keine Projekte geöffnet", Enabled: func() bool { return false }})
	}
	return items
}

// projectItems lists the open project tabs for the Fenster menu; the active one is checked.
func (sh *Shell) projectItems() []command.Item {
	var items []command.Item
	for _, p := range sh.projects {
		q := p
		label := q.sess.Project.Name
		if q.sess.Dirty {
			label += " •"
		}
		checked := 1
		if q == sh.active && !sh.showManager {
			checked = 2
		}
		items = append(items, command.Item{Label: label, Checked: checked, Run: func() { sh.activate(q) }})
	}
	if len(items) == 0 {
		items = append(items, command.Item{Label: "Kein Projekt geöffnet", Enabled: func() bool { return false }})
	}
	return items
}

// shortDir abbreviates the home folder as "~".
func shortDir(dir string) string {
	if home := homeDir(); home != "" && strings.HasPrefix(dir, home) {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}
