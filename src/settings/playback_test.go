package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlaybackMigration(t *testing.T) {
	cases := []struct {
		name, file string
		maxH       int
	}{
		{"old default 720 with auto", `{"version":2,"playback":{"previewQuality":"auto","maxPreviewHeight":720}}`, 0},
		{"720 chosen with half is kept", `{"version":2,"playback":{"previewQuality":"half","maxPreviewHeight":720}}`, 720},
		{"other cap with auto is kept", `{"version":2,"playback":{"previewQuality":"auto","maxPreviewHeight":540}}`, 540},
		{"new file keeps 720", `{"version":3,"playback":{"previewQuality":"auto","maxPreviewHeight":720}}`, 720},
		{"no version", `{"playback":{"previewQuality":"auto","maxPreviewHeight":720}}`, 0},
		{"negative cap", `{"version":3,"playback":{"maxPreviewHeight":-5}}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dir, _ := Dir()
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(c.file), 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := Load()
			if err != nil || s.Playback.MaxPreviewH != c.maxH {
				t.Fatalf("maxH %d err %v, want %d", s.Playback.MaxPreviewH, err, c.maxH)
			}
		})
	}
}

func TestPlaybackDefaultsFillMissingFields(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir, _ := Dir()
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"version":2,"playback":{"previewQuality":"bogus","proxyMode":"x","hwDecode":"y"}}`), 0o644)
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	p := s.Playback
	if p.PreviewQuality != "auto" || p.ProxyMode != "auto" || p.HwDecode != "auto" || p.ProxyCapGB != 10 || p.BackgroundCache || p.Stats || p.CacheMB != 0 {
		t.Fatalf("%+v", p)
	}
	d := Defaults().Playback
	if d.MaxPreviewH != 0 || d.ProxyCapGB != 10 || d.BackgroundCache {
		t.Fatalf("defaults %+v", d)
	}
}

func TestStatsPersists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if err := Update(func(s *Settings) { s.Playback.Stats = true; s.Playback.CacheMB = 512 }); err != nil {
		t.Fatal(err)
	}
	s, _ := Load()
	if !s.Playback.Stats || s.Playback.CacheMB != 512 {
		t.Fatalf("%+v", s.Playback)
	}
}
