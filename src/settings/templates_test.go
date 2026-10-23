package settings

import (
	"strings"
	"testing"

	"framestudio-proto/src/model"
)

func TestTemplateCRUDPersists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Update(func(s *Settings) { *s = Defaults() }) })

	var added Template
	var addErr error
	if err := Update(func(s *Settings) {
		added, addErr = AddTemplate(s, "Kino 24p", 2048, 858, model.NTSC24, false)
	}); err != nil || addErr != nil {
		t.Fatal(err, addErr)
	}
	if !strings.HasPrefix(added.ID, "user:") || !added.User {
		t.Fatalf("id %q", added.ID)
	}
	// stored in the file and read back by a fresh Load
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	got := Get().General.Templates
	if len(got) != 1 || got[0].Name != "Kino 24p" || got[0].Width != 2048 || !got[0].FPS.Equal(model.NTSC24) {
		t.Fatalf("not persisted: %+v", got)
	}
	all := Get().AllTemplates()
	if n := len(BuiltinTemplates()); len(all) != n+1 || all[n].ID != added.ID || !all[n].User {
		t.Fatal("user templates follow the built-in ones")
	}

	// duplicate and invalid names
	var err error
	_ = Update(func(s *Settings) { _, err = AddTemplate(s, " kino 24P ", 1280, 720, model.Rate25, false) })
	if err == nil || !strings.Contains(err.Error(), "gibt es schon") {
		t.Errorf("duplicate name: %v", err)
	}
	_ = Update(func(s *Settings) { _, err = AddTemplate(s, "  ", 1280, 720, model.Rate25, false) })
	if err == nil || !strings.Contains(err.Error(), "fehlt") {
		t.Errorf("empty name: %v", err)
	}
	_ = Update(func(s *Settings) { _, err = AddTemplate(s, strings.Repeat("x", 61), 1280, 720, model.Rate25, false) })
	if err == nil {
		t.Error("long name")
	}
	_ = Update(func(s *Settings) { _, err = AddTemplate(s, "Zu groß", 99999, 720, model.Rate25, false) })
	if err == nil {
		t.Error("size is validated")
	}
	_ = Update(func(s *Settings) {
		_, err = AddTemplate(s, "Zu schnell", 1280, 720, model.Rate{Num: 500, Den: 1}, false)
	})
	if err == nil {
		t.Error("rate is validated")
	}
	if len(Get().General.Templates) != 1 {
		t.Fatal("failed adds must not store anything")
	}

	// drop-frame only sticks for 29,97 / 59,94
	var df, nodf Template
	_ = Update(func(s *Settings) {
		df, _ = AddTemplate(s, "NTSC DF", 1920, 1080, model.NTSC30, true)
		nodf, _ = AddTemplate(s, "PAL DF?", 1920, 1080, model.Rate25, true)
	})
	if !Get().TemplateOrDefault(df.ID).DropFrame || Get().TemplateOrDefault(nodf.ID).DropFrame {
		t.Error("drop-frame flag")
	}

	// rename
	_ = Update(func(s *Settings) { err = RenameTemplate(s, added.ID, "Cinemascope") })
	if err != nil {
		t.Fatal(err)
	}
	if Get().General.Templates[0].Name != "Cinemascope" {
		t.Error("renamed")
	}
	_ = Update(func(s *Settings) { err = RenameTemplate(s, added.ID, "NTSC DF") })
	if err == nil {
		t.Error("rename onto an existing name")
	}
	_ = Update(func(s *Settings) { err = RenameTemplate(s, added.ID, "cinemascope") })
	if err != nil {
		t.Errorf("renaming to the same name in another case is fine: %v", err)
	}

	// delete; a default pointing at it falls back
	_ = Update(func(s *Settings) { s.General.DefaultTemplate = added.ID })
	_ = Update(func(s *Settings) { err = DeleteTemplate(s, added.ID) })
	if err != nil || len(Get().General.Templates) != 2 || Get().General.DefaultTemplate != BuiltinTemplates()[0].ID {
		t.Fatalf("delete: %v %+v default %q", err, Get().General.Templates, Get().General.DefaultTemplate)
	}
	_ = Update(func(s *Settings) { err = DeleteTemplate(s, "user:nope") })
	if err == nil {
		t.Error("deleting an unknown template")
	}
}

func TestResolveTemplateForms(t *testing.T) {
	s := Defaults()
	for id, want := range map[string]Template{
		"1080p25":                     {Width: 1920, Height: 1080, FPS: model.Rate25},
		"1080P60":                     {Width: 1920, Height: 1080, FPS: model.Rate60},
		"1080p2997":                   {Width: 1920, Height: 1080, FPS: model.NTSC30},
		"vertical30":                  {Width: 1080, Height: 1920, FPS: model.Rate30},
		"720p25":                      {Width: 1280, Height: 720, FPS: model.Rate25},
		"2160p25":                     {Width: 3840, Height: 2160, FPS: model.Rate25},
		"1080x1920@30":                {Width: 1080, Height: 1920, FPS: model.Rate30},
		"fmt:1440x1080@24000/1001":    {Width: 1440, Height: 1080, FPS: model.NTSC24},
		"fmt:1920x1080@30000/1001+df": {Width: 1920, Height: 1080, FPS: model.NTSC30, DropFrame: true},
		"fmt:1920x1080@25+df":         {Width: 1920, Height: 1080, FPS: model.Rate25},
		"1440p50":                     {Width: 2560, Height: 1440, FPS: model.Rate50},
	} {
		got, ok := s.ResolveTemplate(id)
		if !ok || got.Width != want.Width || got.Height != want.Height || !got.FPS.Equal(want.FPS) || got.DropFrame != want.DropFrame {
			t.Errorf("%q = %+v (%v), want %+v", id, got, ok, want)
		}
	}
	for _, bad := range []string{"", "unsinn", "fmt:1x1@25", "fmt:1920x1080@999", "user:gibtsnicht"} {
		if _, ok := s.ResolveTemplate(bad); ok {
			t.Errorf("%q must not resolve", bad)
		}
		if s.TemplateOrDefault(bad).ID != "1080p25" {
			t.Errorf("%q falls back to the first built-in template", bad)
		}
	}
	if id := FormatID(1920, 1080, model.NTSC30, true); id != "fmt:1920x1080@30000/1001+df" {
		t.Errorf("format id %q", id)
	}
	if id := FormatID(1920, 1080, model.Rate25, true); id != "fmt:1920x1080@25" {
		t.Errorf("drop-frame is dropped for 25 fps: %q", id)
	}
	for _, b := range BuiltinTemplates() { // every built-in is valid and unique
		if err := model.ValidateSettings(model.Settings{Width: b.Width, Height: b.Height, FPS: b.FPS}); err != nil {
			t.Errorf("%s: %v", b.ID, err)
		}
	}
	seen := map[string]bool{}
	for _, b := range BuiltinTemplates() {
		if seen[b.ID] {
			t.Errorf("duplicate id %s", b.ID)
		}
		seen[b.ID] = true
	}
}

func TestResetKeepsUserTemplates(t *testing.T) {
	t.Cleanup(func() { _ = Update(func(s *Settings) { *s = Defaults() }) })
	_ = Update(func(s *Settings) { _, _ = AddTemplate(s, "Behalten", 1280, 720, model.Rate25, false) })
	_ = Reset()
	if len(Get().General.Templates) != 1 {
		t.Error("Reset restores the factory settings but keeps the user's own templates")
	}
}
