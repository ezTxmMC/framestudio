package shell

import (
	"fmt"
	"framestudio-proto/src/ui/settingsui"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
)

// choice is the answer to "Änderungen speichern?".
type choice int

const (
	choiceSave choice = iota
	choiceDiscard
	choiceCancel
)

// resolveClose decides what closing a project needs: save first, then close. A clean
// project closes without saving whatever was answered; cancel keeps it open.
func resolveClose(dirty bool, c choice) (save, closeIt bool) {
	if !dirty {
		return false, true
	}
	switch c {
	case choiceSave:
		return true, true
	case choiceDiscard:
		return false, true
	}
	return false, false
}

// dirtyProjects returns the projects that would lose changes on quit.
func dirtyProjects(ps []*project) []*project {
	var out []*project
	for _, p := range ps {
		if p.sess.Dirty {
			out = append(out, p)
		}
	}
	return out
}

// askSave shows the three-way save prompt; answer receives the choice.
func (sh *Shell) askSave(message, saveLabel string, answer func(choice)) {
	msg := widget.NewLabel(message)
	msg.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustomWithoutButtons("Änderungen speichern?", container.NewGridWrap(fyne.NewSize(380, 64), msg), sh.win)
	done := func(c choice) {
		d.Hide()
		sh.pendingAnswer = nil
		answer(c)
	}
	cancel := widget.NewButton("Abbrechen", func() { done(choiceCancel) })
	discard := widget.NewButton("Verwerfen", func() { done(choiceDiscard) })
	save := widget.NewButton(saveLabel, func() { done(choiceSave) })
	save.Importance = widget.HighImportance
	d.SetButtons([]fyne.CanvasObject{cancel, discard, save})
	sh.pendingAnswer = done
	d.Show()
}

// Save writes p to its file, or into the project library when it has none yet.
func (sh *Shell) Save(p *project) error {
	if p == nil {
		return nil
	}
	path := p.sess.Path
	if path == "" {
		dir, err := store.ProjectsDir()
		if err != nil {
			return err
		}
		path = store.SuggestPath(dir, p.sess.Project.Name)
	}
	return sh.saveTo(p, path)
}

func (sh *Shell) saveTo(p *project, path string) error {
	if err := sh.writeProject(p.sess.Project, path); err != nil {
		return err
	}
	p.savedAt = sh.now()
	p.autosaved = false
	p.saveErr = ""
	p.sess.MarkSaved(path)
	store.AddRecent(path)
	sh.refreshChrome()
	return nil
}

// writeProject writes proj to path on the UI thread, after any autosave of that file
// that is writing right now; older autosaves that have not written yet are skipped.
func (sh *Shell) writeProject(proj *model.Project, path string) error {
	_, err := sh.saves.write(path, sh.saves.next(), func() error { return store.Save(proj, path) })
	return err
}

// discardAutosaves undoes what autosave wrote to p's file after mark (a sequence number
// taken when the save prompt opened) and blocks older pending autosaves, so "Verwerfen"
// really leaves the file as it was.
func (sh *Shell) discardAutosaves(p *project, mark uint64) {
	path := p.sess.Path
	if path == "" {
		return
	}
	sh.saves.block(path) // waits for a write in progress
	for _, j := range p.autosaves {
		if j.path == path && j.landed > mark {
			if err := sh.saves.restore(path, j.prev, j.existed); err != nil {
				fyne.LogError("Automatisch gespeicherte Änderungen verwerfen", err)
			}
			break // the earliest such write holds the state before the prompt
		}
	}
	p.autosaves = nil
}

// saveActive is the "Speichern" command.
func (sh *Shell) saveActive() {
	if sh.active != nil {
		sh.showErr(sh.Save(sh.active))
	}
}

// saveAs asks for a file and saves the active project there.
func (sh *Shell) saveAs() {
	p := sh.active
	if p == nil {
		return
	}
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			sh.showErr(err)
			return
		}
		if w == nil {
			return
		}
		path := w.URI().Path()
		_ = w.Close()
		if !strings.EqualFold(filepath.Ext(path), store.Extension) {
			path += store.Extension
		}
		sh.showErr(sh.saveTo(p, path))
	}, sh.win)
	d.SetFileName(store.SafeName(p.sess.Project.Name) + store.Extension)
	d.SetFilter(storage.NewExtensionFileFilter([]string{store.Extension}))
	sh.setDialogDir(d, p.sess.Path)
	d.Resize(fyne.NewSize(900, 600))
	d.Show()
}

// setDialogDir starts a file dialog in the folder of path, or in the project library.
func (sh *Shell) setDialogDir(d *dialog.FileDialog, path string) {
	dir := filepath.Dir(path)
	if path == "" {
		var err error
		if dir, err = store.ProjectsDir(); err != nil {
			return
		}
	}
	if l, err := storage.ListerForURI(storage.NewFileURI(dir)); err == nil {
		d.SetLocation(l)
	}
}

// closeActive is the "Projekt schließen" command.
func (sh *Shell) closeActive() {
	if sh.active != nil && !sh.showManager {
		sh.closeProject(sh.active)
	}
}

// restart quits through the normal flow (busy and save prompts, layout, audio) and
// starts the program again afterwards. Cancelling a prompt cancels the restart: a later
// Quit is a plain quit.
func (sh *Shell) restart() {
	sh.restarting = true
	sh.quit()
}

// Quit closes the window, asking first about running exports and then once about all
// unsaved projects.
func (sh *Shell) Quit() {
	sh.restarting = false
	sh.quit()
}

func (sh *Shell) quit() {
	var busy []string
	for _, p := range sh.projects {
		if b := projectBusy(p); b != "" {
			if len(sh.projects) > 1 {
				b = "„" + p.sess.Project.Name + "“: " + b
			}
			busy = append(busy, b)
		}
	}
	if len(busy) > 0 {
		sh.confirmBusy(strings.Join(busy, "\n")+".\nAbbrechen und beenden?", "Abbrechen und beenden", sh.quitSaved)
		return
	}
	sh.quitSaved()
}

// quitSaved quits, asking once about all unsaved projects.
// Without the ConfirmClose setting, projects with a file are saved without asking (as
// when closing a tab); only untitled ones are asked about.
func (sh *Shell) quitSaved() {
	dirty := dirtyProjects(sh.projects)
	if !settings.Get().General.ConfirmClose {
		var ask []*project
		for _, p := range dirty {
			if p.sess.Path == "" {
				ask = append(ask, p)
				continue
			}
			if err := sh.Save(p); err != nil {
				sh.showErr(err)
				return
			}
		}
		dirty = ask
	}
	if len(dirty) == 0 {
		sh.shutdown()
		return
	}
	names := make([]string, len(dirty))
	for i, p := range dirty {
		names[i] = "„" + p.sess.Project.Name + "“"
	}
	msg := fmt.Sprintf("%s hat ungespeicherte Änderungen.", names[0])
	label := "Speichern"
	if len(dirty) > 1 {
		msg = fmt.Sprintf("%d Projekte haben ungespeicherte Änderungen: %s.", len(dirty), strings.Join(names, ", "))
		label = "Alle speichern"
	}
	mark := sh.saves.next()
	sh.askSave(msg, label, func(c choice) {
		if c == choiceDiscard {
			for _, p := range dirty {
				sh.discardAutosaves(p, mark)
			}
		}
		save, quit := resolveClose(true, c)
		if save {
			for _, p := range dirty {
				if err := sh.Save(p); err != nil {
					sh.showErr(err)
					return
				}
			}
		}
		if quit {
			sh.shutdown()
		}
	})
}

// shutdown remembers the open projects for the next start, lets the workspace save its
// layout and close the detached windows, releases all projects and closes the window.
func (sh *Shell) shutdown() {
	sh.shutdownDone = true
	if sh.restarting {
		settingsui.RequestRestart() // quitting is certain now; application.Run restarts
	}
	if sh.menuBar != nil {
		sh.menuBar.Close()
	}
	sh.saveLastOpen()
	sh.ws.Shutdown()
	settingsui.Close() // an open settings window would keep the run loop alive
	sh.releaseAll()
	sh.dispose()
	sh.closeWindow()
}

// releaseAll stops the background work of every open project in dependency order:
// first the pages (exports, previews, viewers), giving cancelled exports a moment to
// remove partial files; then each project's live audio engine. The shared media cache
// is closed last, by Run, after the window is gone. Safe to call twice.
func (sh *Shell) releaseAll() {
	sh.mgr.cancelThumbnails()
	sh.toast.stopTimer()
	ps := slices.Clone(sh.projects)
	var waiters []page.Waiter
	for _, p := range ps {
		waiters = append(waiters, sh.closePages(p)...)
	}
	deadline := time.Now().Add(shutdownWait)
	for _, w := range waiters {
		w.Wait(max(time.Until(deadline), 0))
	}
	for _, p := range ps {
		sh.release(p)
	}
	sh.active = nil
}

// shutdownWait bounds how long quitting waits for background cleanup.
const shutdownWait = 3 * time.Second

// closeNowQuiet releases p without switching tabs or showing the manager.
func (sh *Shell) closeNowQuiet(p *project) {
	sh.closePages(p)
	sh.release(p)
}

// closePages closes p's pages (cancelling exports and previews) and its media decodes,
// and returns the pages that finish cleanup in the background.
func (sh *Shell) closePages(p *project) []page.Waiter {
	if p == sh.active {
		sh.captureLayouts(p) // the next tab and the saved layout keep the arrangement
	}
	var waiters []page.Waiter
	for _, id := range slices.Sorted(maps.Keys(p.pages)) {
		pg := p.pages[id]
		pg.Close()
		if w, ok := pg.(page.Waiter); ok {
			waiters = append(waiters, w)
		}
	}
	if sh.findOtherUser(p) == nil {
		for _, m := range p.sess.Project.Media {
			sh.cache.Cancel(m.ID) // stop its thumbnail and waveform decodes
		}
	}
	return waiters
}

// release stops p's playback clock and audio engine and drops the tab.
func (sh *Shell) release(p *project) {
	sh.unbindProject(p)
	p.stopPlay()
	p.unsub()
	sh.projects = slices.DeleteFunc(sh.projects, func(q *project) bool { return q == p })
	if sh.active == p {
		sh.active = nil
	}
}

// findOtherUser returns another open project that shares media IDs with p (a duplicate
// opened twice), whose decodes must keep running.
func (sh *Shell) findOtherUser(p *project) *project {
	ids := map[string]bool{}
	for _, m := range p.sess.Project.Media {
		ids[m.ID] = true
	}
	for _, q := range sh.projects {
		if q == p {
			continue
		}
		for _, m := range q.sess.Project.Media {
			if ids[m.ID] {
				return q
			}
		}
	}
	return nil
}

// autosaveJob is one background save: a snapshot cloned on the UI thread.
type autosaveJob struct {
	proj *project
	snap *model.Project
	path string
	rev  int
	seq  uint64 // when the snapshot was taken
	log  *saveLog

	// Set by the writer under the file lock.
	wrote   bool
	landed  uint64 // sequence number when the write finished
	prev    []byte // file content before the write
	existed bool
	err     error
}

// StartAutosave writes dirty projects that have a file every interval (0 = off); the
// returned func stops it. A changed General.AutosaveSeconds setting applies at once.
func (sh *Shell) StartAutosave(interval time.Duration) (stop func()) {
	quit := make(chan struct{})
	done := make(chan struct{})
	set := sh.autosaveSet
	go func() {
		defer close(done)
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		tick := func(d time.Duration) <-chan time.Time {
			if d <= 0 {
				return nil // off: never fires
			}
			t.Reset(d)
			return t.C
		}
		c := tick(interval)
		for {
			select {
			case <-quit:
				return
			case d := <-set:
				c = tick(d)
			case <-c:
				var jobs []*autosaveJob
				kit.DoAndWait(func() { jobs = sh.autosaveJobs() })
				if len(jobs) == 0 {
					continue
				}
				runAutosave(jobs)
				kit.Do(func() { sh.finishAutosave(jobs) })
			}
		}
	}()
	return func() {
		close(quit)
		select { // let a write in progress finish its rename
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

// autosaveJobs snapshots the projects to autosave. UI thread only.
func (sh *Shell) autosaveJobs() []*autosaveJob {
	var jobs []*autosaveJob
	for _, p := range sh.projects {
		if p.sess.Dirty && p.sess.Path != "" {
			j := &autosaveJob{proj: p, snap: p.sess.Project.Clone(), path: p.sess.Path, rev: p.rev,
				seq: sh.saves.next(), log: sh.saves}
			jobs = append(jobs, j)
			p.autosaves = append(p.autosaves, j)
			if n := len(p.autosaves); n > 4 {
				p.autosaves = p.autosaves[n-4:]
			}
		}
	}
	return jobs
}

// runAutosave writes the snapshots, skipping any that a newer save made obsolete; safe
// off the UI thread.
func runAutosave(jobs []*autosaveJob) {
	for _, j := range jobs {
		_, j.err = j.log.write(j.path, j.seq, func() error {
			prev, err := os.ReadFile(j.path)
			j.prev, j.existed = prev, err == nil
			if err := store.Save(j.snap, j.path); err != nil {
				return err
			}
			j.wrote, j.landed = true, j.log.next()
			return nil
		})
	}
}

// finishAutosave marks projects saved unless they were edited, moved or closed meanwhile.
func (sh *Shell) finishAutosave(jobs []*autosaveJob) {
	for _, j := range jobs {
		p := j.proj
		if !slices.Contains(sh.projects, p) || p.sess.Path != j.path {
			continue
		}
		if j.err != nil {
			p.saveErr = "Automatisches Speichern fehlgeschlagen"
			fyne.LogError("Automatisches Speichern", j.err)
			continue
		}
		p.saveErr = ""
		if j.wrote && p.rev == j.rev && p.sess.Dirty {
			p.savedAt = sh.now()
			p.autosaved = true
			p.sess.MarkSaved(j.path)
		}
	}
	sh.refreshChrome()
}
