package shell

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/model"
	"framestudio-proto/src/settings"
)

func closeTop(w fyne.Window) {
	if o := w.Canvas().Overlays().Top(); o != nil {
		w.Canvas().Overlays().Remove(o)
	}
}

func TestNewProjectDialogTemplates(t *testing.T) {
	sh, w, _ := newTestShell(t)
	sh.newProjectDialog()
	n := sh.npd
	if w.Canvas().Overlays().Top() == nil {
		t.Fatal("dialog not shown")
	}
	// the first built-in template is the default: 1080p25
	if n.cur.Width != 1920 || n.cur.Height != 1080 || !n.cur.FPS.Equal(model.Rate25) {
		t.Fatalf("default %+v", n.cur)
	}
	if got := len(settings.BuiltinTemplates()); got < 14 {
		t.Fatalf("only %d built-in templates", got)
	}

	// pick "1080p 23,976" from the list: the picker follows
	idx := -1
	for i, it := range n.tl.items {
		if it.ID == "1080p2398" {
			idx = i
		}
	}
	n.tl.list.Select(idx)
	if !n.cur.FPS.Equal(model.NTSC24) || n.picker.Rate.Select().Selected != model.NTSC24.Label() {
		t.Fatalf("template not applied: %+v %q", n.cur, n.picker.Rate.Select().Selected)
	}
	// editing a value leaves the template list: custom values
	n.picker.Res.Width.SetText("1900")
	if _, ok := n.tl.current(); ok || n.cur.Width != 1900 || !strings.Contains(n.status.Text, "Eigene Werte") {
		t.Errorf("custom values: %+v status %q", n.cur, n.status.Text)
	}
	// an invalid value blocks creating and saving
	n.picker.Res.Width.SetText("17x")
	if n.curErr == nil || !n.create.Disabled() || !n.saveTpl.Disabled() {
		t.Error("invalid width must disable Anlegen and the template button")
	}
	n.picker.Res.Width.SetText("1900")
	if n.create.Disabled() {
		t.Error("valid again")
	}
	n.name.SetText("  ")
	if !n.create.Disabled() {
		t.Error("an empty name disables Anlegen")
	}
	n.name.SetText("Mein Film")

	// custom frame rate 29,97 typed, then created: values arrive in the project
	n.picker.Rate.Select().SetSelected("Eigene…")
	n.picker.Rate.CustomEntry().SetText("29,97")
	n.picker.Res.Width.SetText("1920")
	n.create.OnTapped()
	pr := sh.Active().Project
	if pr.Name != "Mein Film" || pr.Settings.Width != 1920 || !pr.Rate().Equal(model.NTSC30) || pr.DropFrame() {
		t.Fatalf("project %+v", pr.Settings)
	}
	// the last used format is the next default
	if id := settings.Get().General.DefaultTemplate; id != "1080p2997" {
		t.Errorf("default template %q", id)
	}
	closeTop(w)

	// a custom format with drop-frame is remembered as a format ID
	sh.newProjectDialog()
	n = sh.npd
	if !n.cur.FPS.Equal(model.NTSC30) {
		t.Fatalf("dialog must open with the remembered format: %+v", n.cur)
	}
	n.picker.Res.Width.SetText("1440")
	if !n.picker.Rate.DropEnabled() {
		t.Fatal("drop-frame must be possible at 29,97")
	}
	n.picker.Rate.SetDropFrame(true)
	n.submit()
	if sh.Active().Project.Settings.Width != 1440 || !sh.Active().Project.DropFrame() {
		t.Fatalf("drop-frame project: %+v", sh.Active().Project.Settings)
	}
	if id := settings.Get().General.DefaultTemplate; id != "fmt:1440x1080@30000/1001+df" {
		t.Errorf("remembered %q", id)
	}
	sh.newProjectDialog()
	if c := sh.npd.cur; c.Width != 1440 || !c.DropFrame {
		t.Errorf("remembered format not restored: %+v", c)
	}
	closeTop(w)
}

func TestNewProjectUserTemplatesCRUD(t *testing.T) {
	sh, w, _ := newTestShell(t)
	sh.newProjectDialog()
	n := sh.npd
	builtin := len(settings.BuiltinTemplates())

	n.picker.Res.Width.SetText("2048")
	n.picker.Res.Height.SetText("858")
	n.picker.Rate.Select().SetSelected(model.Rate24.Label())
	if err := n.saveTemplate("  "); err == nil {
		t.Error("an empty name must be refused")
	}
	if err := n.saveTemplate("Kino 24p"); err != nil {
		t.Fatal(err)
	}
	if len(n.tl.items) != builtin+1 || n.tl.items[builtin].Name != "Kino 24p" {
		t.Fatalf("list not updated: %d items", len(n.tl.items))
	}
	if cur, ok := n.tl.current(); !ok || !cur.User || n.renameBtn.Disabled() || n.deleteBtn.Disabled() {
		t.Error("the new template is selected and can be renamed and deleted")
	}
	if err := n.saveTemplate("kino 24P"); err == nil || !strings.Contains(err.Error(), "gibt es schon") {
		t.Errorf("duplicate: %v", err)
	}
	if got := settings.Get().General.Templates; len(got) != 1 || got[0].Width != 2048 || !got[0].FPS.Equal(model.Rate24) {
		t.Fatalf("stored %+v", got)
	}

	// choosing a built-in template disables rename and delete
	n.tl.list.Select(0)
	if !n.renameBtn.Disabled() || !n.deleteBtn.Disabled() {
		t.Error("built-in templates cannot be changed")
	}
	if err := n.renameTemplate("X"); err == nil {
		t.Error("rename of a built-in template")
	}
	n.tl.list.Select(builtin)
	if err := n.renameTemplate("Cinemascope"); err != nil {
		t.Fatal(err)
	}
	if settings.Get().General.Templates[0].Name != "Cinemascope" || n.tl.items[builtin].Name != "Cinemascope" {
		t.Error("rename")
	}
	if n.cur.Width != 2048 {
		t.Error("renaming keeps the values")
	}
	n.deleteTemplate()
	if len(settings.Get().General.Templates) != 0 || len(n.tl.items) != builtin {
		t.Error("delete")
	}
	closeTop(w)
}

func TestProjectSettingsApplyIsOneUndo(t *testing.T) {
	sh, w, _ := newTestShell(t)
	sh.newProject("PS", Templates[0])
	edit(t, sh) // 2 clips
	sess := sh.Active()
	for i := 0; i < 12; i++ { // 14 clips
		c := model.NewClip(model.KindColor, "C", 1000+i*10, 10)
		if err := sess.Edit("Clip legen", func(p *model.Project) error { return p.PlaceClip(p.Tracks[0].ID, c) }); err != nil {
			t.Fatal(err)
		}
	}
	nClips := 0
	for _, tr := range sess.Project.Tracks {
		nClips += len(tr.Clips)
	}

	sh.projectSettings()
	u := sh.psd
	if u == nil || w.Canvas().Overlays().Top() == nil {
		t.Fatal("dialog not shown")
	}
	if !u.apply.Disabled() || !strings.Contains(u.summary.Text, "Keine Änderung") {
		t.Fatalf("nothing changed yet: %q", u.summary.Text)
	}
	u.picker.Rate.Select().SetSelected(model.NTSC30.Label())
	u.picker.Res.Width.SetText("1280")
	u.picker.Res.Height.SetText("720")
	u.picker.Rate.SetDropFrame(true)
	u.pickBackground(projectBackColors[2])
	if u.apply.Disabled() {
		t.Fatal("changes enable Anwenden")
	}
	want := fmt.Sprintf("Bildrate 25 → 29,97 fps: %d Clips werden auf das Bildraster angepasst", nClips)
	if !strings.Contains(u.summary.Text, want) || !strings.Contains(u.summary.Text, "1920×1080 → 1280×720") ||
		!strings.Contains(u.summary.Text, "Drop-Frame") || !strings.Contains(u.summary.Text, "Hintergrundfarbe") {
		t.Errorf("summary %q (want %q)", u.summary.Text, want)
	}
	if !u.warnBox.Visible() || !strings.Contains(u.warn.Text, "gerundet") {
		t.Errorf("25 → 29,97 must warn about rounding: %q", u.warn.Text)
	}
	sess.Playhead = 50
	u.Apply()
	st := sess.Project.Settings
	if st.Width != 1280 || st.Height != 720 || !st.FPS.Equal(model.NTSC30) || !st.DropFrame || st.Background != projectBackColors[2] {
		t.Fatalf("settings %+v", st)
	}
	n := 0
	for _, tr := range sess.Project.Tracks {
		n += len(tr.Clips)
	}
	if n != nClips {
		t.Error("no clip is lost")
	}
	if sess.UndoLabel() != "Projekteinstellungen ändern" {
		t.Errorf("undo label %q", sess.UndoLabel())
	}
	sess.Undo()
	st = sess.Project.Settings
	if st.Width != 1920 || !st.FPS.Equal(model.Rate25) || st.DropFrame || st.Background == projectBackColors[2] || sess.Playhead != 50 {
		t.Fatalf("one undo restores everything: %+v playhead %d", st, sess.Playhead)
	}
	if sess.UndoLabel() == "Projekteinstellungen ändern" {
		t.Error("Anwenden must be a single undo step")
	}
	closeTop(w)

	// an exact rate change shows no warning; an invalid value disables Anwenden
	sh.projectSettings()
	u = sh.psd
	u.picker.Rate.Select().SetSelected(model.Rate50.Label())
	if u.warnBox.Visible() {
		t.Errorf("25 → 50 is exact: %q", u.warn.Text)
	}
	u.picker.Res.Width.SetText("99999")
	if !u.apply.Disabled() || u.summary.Text != "" {
		t.Error("invalid width disables Anwenden")
	}
	u.Apply()
	if !sess.Project.Rate().Equal(model.Rate25) {
		t.Error("Apply with an invalid value must do nothing")
	}
	closeTop(w)
}

func TestProjectSettingsMatchFirstMedia(t *testing.T) {
	sh, w, _ := newTestShell(t)
	sh.newProject("M", Templates[0])
	sess := sh.Active()
	_ = sess.Edit("Medium", func(p *model.Project) error {
		p.AddMedia(model.MediaItem{Name: "v.mp4", Kind: model.KindVideo, Width: 1280, Height: 720, FPS: model.NTSC24, Duration: 5 * time.Second})
		return nil
	})
	sh.projectSettings()
	u := sh.psd
	u.picker.MatchMedia()
	if u.cur.Width != 1280 || !u.cur.FPS.Equal(model.NTSC24) || u.apply.Disabled() {
		t.Fatalf("Wie erstes Medium: %+v", u.cur)
	}
	closeTop(w)
}

func TestFormatDialogShots(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT")
	}
	for _, size := range []fyne.Size{{Width: 1440, Height: 900}, {Width: 1280, Height: 800}} {
		sh, w, _ := newTestShell(t)
		w.Resize(size)
		_ = settings.Update(func(s *settings.Settings) {
			_, _ = settings.AddTemplate(s, "Kino 24p", 2048, 858, model.Rate24, false)
		})
		sh.newProjectDialog()
		sh.npd.tl.list.Select(len(sh.npd.tl.items) - 1)
		capture(t, w, fmt.Sprintf("/tmp/fs-r6-newproject-%d.png", int(size.Width)))
		closeTop(w)

		sh.newProject("Test", Templates[0])
		edit(t, sh)
		sh.projectSettings()
		u := sh.psd
		u.picker.Rate.Select().SetSelected(model.NTSC30.Label())
		u.picker.Res.Width.SetText("1280")
		u.picker.Res.Height.SetText("720")
		capture(t, w, fmt.Sprintf("/tmp/fs-r6-projectsettings-%d.png", int(size.Width)))
		closeTop(w)
	}
}
