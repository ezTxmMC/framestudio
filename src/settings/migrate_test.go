package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowButtonsMigration(t *testing.T) {
	cases := []struct{ name, file, want string }{
		{"old default mac", `{"version":1,"display":{"windowButtons":"mac"}}`, "auto"},
		{"no version mac", `{"display":{"windowButtons":"mac"}}`, "auto"},
		{"win kept", `{"version":1,"display":{"windowButtons":"win"}}`, "win"},
		{"linux kept", `{"version":1,"display":{"windowButtons":"linux"}}`, "linux"},
		{"new mac kept", `{"version":2,"display":{"windowButtons":"mac"}}`, "mac"},
		{"partial without buttons", `{"version":1,"display":{"accent":"teal"}}`, "auto"},
		{"empty object", `{}`, "auto"},
		{"unknown value kept", `{"version":1,"display":{"windowButtons":"zzz"}}`, "zzz"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dir, _ := Dir()
			p := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(p, []byte(c.file), 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := Load()
			if err != nil || s.Display.WindowButtons != c.want || s.Version != Version {
				t.Fatalf("got %q v%d err %v, want %q", s.Display.WindowButtons, s.Version, err, c.want)
			}
			// Load does not rewrite the file; the next Update writes the new version
			b, _ := os.ReadFile(p)
			if string(b) != c.file {
				t.Fatalf("Load rewrote the file: %s", b)
			}
			if err := Update(func(s *Settings) { s.Display.Compact = true }); err != nil {
				t.Fatal(err)
			}
			b, _ = os.ReadFile(p)
			if !strings.Contains(string(b), `"version": 2`) {
				t.Fatalf("version not written: %s", b)
			}
		})
	}
}

func TestMigrationDamagedFileFallsBackToAuto(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir, _ := Dir()
	p := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(p, []byte(`{"version":1,"display":{"windowButtons":"mac"`), 0o644)
	s, err := Load()
	if err == nil || s.Display.WindowButtons != "auto" || s.Version != Version {
		t.Fatalf("damaged: %q v%d %v", s.Display.WindowButtons, s.Version, err)
	}
	if _, e := os.Stat(p + ".beschaedigt"); e != nil {
		t.Fatal("damaged file not kept")
	}
}
