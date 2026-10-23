package settingsui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/settings"
)

func TestTemplateSettingsFields(t *testing.T) {
	f := newFixture(t, "general")
	sel := f.field("Vorlage für neue Projekte").input.(*widget.Select)
	builtin := len(settings.BuiltinTemplates())
	if len(sel.Options) != builtin || !strings.Contains(sel.Selected, "1080p HD") {
		t.Fatalf("options %d selected %q", len(sel.Options), sel.Selected)
	}
	_ = settings.Update(func(s *settings.Settings) {
		_, _ = settings.AddTemplate(s, "Kino 24p", 2048, 858, model.Rate24, false)
	})
	if len(sel.Options) != builtin+1 || !strings.Contains(sel.Options[builtin], "Kino 24p · 2048×858 · 24 fps (eigene)") {
		t.Fatalf("user template missing from the choice: %v", sel.Options)
	}
	sel.SetSelectedIndex(builtin)
	if id := settings.Get().General.DefaultTemplate; !strings.HasPrefix(id, "user:") {
		t.Fatalf("default %q", id)
	}
	// a remembered format shows up as "Zuletzt verwendet"
	_ = settings.Update(func(s *settings.Settings) { s.General.DefaultTemplate = "fmt:1440x1080@30000/1001" })
	if !strings.Contains(sel.Selected, "Zuletzt verwendet · 1440×1080 · 29,97 fps") {
		t.Errorf("remembered format shown as %q", sel.Selected)
	}

	// the list of own templates with rename and delete
	list := f.field("Eigene Vorlagen")
	if list.input == nil {
		t.Fatal("no template list")
	}
	f.w.renameTemplate(settings.Get().General.Templates[0])
	f.w.confirm = func(_, _, _ string, yes func()) { yes() }
	f.w.set(func(s *settings.Settings) { _ = settings.DeleteTemplate(s, s.General.Templates[0].ID) })
	if len(settings.Get().General.Templates) != 0 {
		t.Error("delete")
	}
	// resetting the category keeps the user's templates
	_ = settings.Update(func(s *settings.Settings) { _, _ = settings.AddTemplate(s, "Bleibt", 1280, 720, model.Rate25, false) })
	f.w.resetCategory(f.w.cur)
	if len(settings.Get().General.Templates) != 1 {
		t.Error("reset of Allgemein must not delete the user's templates")
	}
}
