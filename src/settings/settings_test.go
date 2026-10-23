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
