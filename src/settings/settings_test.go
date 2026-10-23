package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUpdatePersist(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if s, err := Load(); err != nil || s.General.AutosaveSeconds != 90 {
		t.Fatalf("defaults expected: %v %+v", err, s.General)
	}
	var seen Settings
	unsub := Subscribe(func(s Settings) { seen = s })
	defer unsub()
	if err := Update(func(s *Settings) { s.Audio.LatencyMS = 80; s.Keymap["file.save"] = "Ctrl+Alt+S" }); err != nil {
		t.Fatal(err)
	}
	if seen.Audio.LatencyMS != 80 {
		t.Fatal("subscriber not called")
	}
	s, _ := Load()
	if s.Audio.LatencyMS != 80 || s.Keymap["file.save"] != "Ctrl+Alt+S" {
		t.Fatalf("not persisted: %+v", s.Audio)
	}
	// partial file keeps defaults for missing fields
	dir, _ := Dir()
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"audio":{"latencyMs":5}}`), 0o644)
	s, _ = Load()
	if s.Audio.LatencyMS != 5 || s.General.AutosaveSeconds != 90 || s.Display.Platform != "auto" {
		t.Fatalf("partial merge wrong %+v", s)
	}
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{broken`), 0o644)
	if _, err := Load(); err == nil {
		t.Fatal("broken file must report an error")
	}
}

// An unreadable settings file: changes still apply but Update says they are not saved,
// and the file is left alone.
func TestUnreadableFileBlocksSaving(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every file")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir, _ := Dir()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(`{"audio":{"latencyMs":5}}`), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	if _, err := Load(); err == nil {
		t.Fatal("unreadable file loaded")
	}
	if !Loaded() || Blocked() == nil {
		t.Fatal("blocked state not recorded")
	}
	err := Update(func(s *Settings) { s.Audio.LatencyMS = 9 })
	if err == nil || Get().Audio.LatencyMS != 9 {
		t.Fatalf("update: %v, value %d", err, Get().Audio.LatencyMS)
	}
	_ = os.Chmod(p, 0o644)
	if b, _ := os.ReadFile(p); string(b) != `{"audio":{"latencyMs":5}}` {
		t.Fatalf("file overwritten: %s", b)
	}
	if _, err := Load(); err != nil || Blocked() != nil {
		t.Fatalf("readable again: %v %v", err, Blocked())
	}
}
