package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetProjectsDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	def, err := ProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(t.TempDir(), "Videos", "Projekte")
	SetProjectsDir(custom)
	t.Cleanup(func() { SetProjectsDir("") })
	got, err := ProjectsDir()
	if err != nil || got != custom {
		t.Fatalf("custom dir = %q, %v", got, err)
	}
	if fi, err := os.Stat(custom); err != nil || !fi.IsDir() {
		t.Fatal("custom dir not created")
	}

	home, _ := os.UserHomeDir()
	SetProjectsDir("~/fs-test-nicht-anlegen")
	dirMu.Lock()
	tilde := projectsDir
	dirMu.Unlock()
	if tilde != filepath.Join(home, "fs-test-nicht-anlegen") {
		t.Errorf("tilde expanded to %q", tilde)
	}

	// A folder that cannot be created reports a clear error.
	file := filepath.Join(t.TempDir(), "datei")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	SetProjectsDir(filepath.Join(file, "unter"))
	if _, err := ProjectsDir(); err == nil {
		t.Error("a folder below a file must fail")
	}

	SetProjectsDir("")
	if got, _ := ProjectsDir(); got != def {
		t.Errorf("reset gave %q, want %q", got, def)
	}
}
