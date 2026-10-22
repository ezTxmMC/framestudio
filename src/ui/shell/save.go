package shell

import (
	"fmt"
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
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/kit"
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
	if err := store.Save(p.sess.Project, path); err != nil {
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

// Quit closes the window, asking once about all unsaved projects.
func (sh *Shell) Quit() {
	dirty := dirtyProjects(sh.projects)
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
	sh.askSave(msg, label, func(c choice) {
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

// shutdown releases all projects and closes the window.
func (sh *Shell) shutdown() {
	for _, p := range slices.Clone(sh.projects) {
		sh.closeNowQuiet(p)
	}
	sh.closeWindow()
}

// closeNowQuiet releases p without switching tabs or showing the manager.
func (sh *Shell) closeNowQuiet(p *project) {
	for _, pg := range p.pages {
		pg.Close()
	}
	p.stopPlay()
	p.unsub()
	sh.projects = slices.DeleteFunc(sh.projects, func(q *project) bool { return q == p })
	if sh.active == p {
		sh.active = nil
	}
}

// autosaveJob is one background save: a snapshot cloned on the UI thread.
type autosaveJob struct {
	proj *project
	snap *model.Project
	path string
	rev  int
	err  error
}

// StartAutosave writes dirty projects that have a file every interval; the returned
// func stops it.
func (sh *Shell) StartAutosave(interval time.Duration) (stop func()) {
	quit := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
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
	return func() { close(quit) }
}

// autosaveJobs snapshots the projects to autosave. UI thread only.
func (sh *Shell) autosaveJobs() []*autosaveJob {
	var jobs []*autosaveJob
	for _, p := range sh.projects {
		if p.sess.Dirty && p.sess.Path != "" {
			jobs = append(jobs, &autosaveJob{proj: p, snap: p.sess.Project.Clone(), path: p.sess.Path, rev: p.rev})
		}
	}
	return jobs
}

// runAutosave writes the snapshots; safe off the UI thread.
func runAutosave(jobs []*autosaveJob) {
	for _, j := range jobs {
		j.err = store.Save(j.snap, j.path)
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
		if p.rev == j.rev {
			p.savedAt = sh.now()
			p.autosaved = true
			p.sess.MarkSaved(j.path)
		}
	}
	sh.refreshChrome()
}
