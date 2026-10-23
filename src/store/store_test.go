package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"framestudio-proto/src/model"
)

func sample(name string) *model.Project {
	p := model.NewProject(name)
	c := model.NewClip(model.KindColor, "Rot", 10, 50)
	if err := p.PlaceClip(p.Tracks[0].ID, c); err != nil {
		panic(err)
	}
	p.AddMarker(12, "Schnitt")
	return p
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := sample("Urlaub")
	path := filepath.Join(dir, "urlaub.fsproj")
	if err := Save(p, path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Urlaub" || got.Duration() != 60 || len(got.Markers) != 1 || got.ID != p.ID {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if !got.Modified.Equal(p.Modified) {
		t.Errorf("modified %v, want %v", got.Modified, p.Modified)
	}
	// No temp files are left behind.
	des, _ := os.ReadDir(dir)
	if len(des) != 1 {
		t.Errorf("dir has %d entries, want 1", len(des))
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "\n  \"name\": \"Urlaub\"") {
		t.Error("file is not indented JSON")
	}
}

func TestLoadMigratesAndRejects(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := write("old.fsproj", `{"name":"Alt","settings":{"width":1280,"height":720,"fps":25},"tracks":[{"id":"t","name":"V1","kind":"video"}]}`)
	p, err := Load(old)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != model.FormatVersion || p.Media == nil || p.Markers == nil || p.Tracks[0].Clips == nil || p.ID == "" {
		t.Errorf("not migrated: %+v", p)
	}

	newer := write("new.fsproj", `{"version":99,"name":"Neu","settings":{"width":1280,"height":720,"fps":25}}`)
	if _, err := Load(newer); err == nil || !strings.Contains(err.Error(), "neueren Frame-Studio-Version") {
		t.Errorf("newer version: %v", err)
	}
	bad := write("bad.fsproj", `{"version":1,"settings":{"width":0,"height":0,"fps":0}}`)
	if _, err := Load(bad); err == nil {
		t.Error("invalid settings accepted")
	}
	if _, err := Load(write("junk.fsproj", "kein json")); err == nil {
		t.Error("junk accepted")
	}
	if _, err := Load(filepath.Join(dir, "fehlt.fsproj")); err == nil {
		t.Error("missing file accepted")
	}
}

func TestListNewestFirstIgnoresBroken(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i, name := range []string{"A", "B", "C"} {
		path := filepath.Join(dir, name+Extension)
		if err := Save(sample(name), path); err != nil {
			t.Fatal(err)
		}
		mt := now.Add(time.Duration(i-5) * time.Minute)
		if name == "A" {
			mt = now // A is newest
		}
		if err := os.Chtimes(path, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "kaputt.fsproj"), []byte("{"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "notiz.txt"), []byte("x"), 0o644)

	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name)
		if e.FPS != model.Rate25 || e.Duration != 60 {
			t.Errorf("%s: fps %v dur %d", e.Name, e.FPS, e.Duration)
		}
	}
	if want := []string{"A", "C", "B"}; !reflect.DeepEqual(names, want) {
		t.Errorf("order %v, want %v", names, want)
	}
	if _, err := List(filepath.Join(dir, "nope")); err == nil {
		t.Error("missing dir should fail")
	}
}

func TestRecentPersistence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 20; i++ {
		p := filepath.Join(dir, string(rune('a'+i))+Extension)
		_ = os.WriteFile(p, []byte("{}"), 0o644)
		paths = append(paths, p)
		AddRecent(p)
	}
	got := Recent()
	if len(got) != maxRecent || got[0] != paths[19] || got[14] != paths[5] {
		t.Fatalf("recent = %v", got)
	}
	AddRecent(paths[10])
	if got := Recent(); got[0] != paths[10] || len(got) != maxRecent {
		t.Errorf("re-add did not move to top: %v", got)
	}
	os.Remove(paths[19])
	for _, p := range Recent() {
		if p == paths[19] {
			t.Error("missing file listed")
		}
	}
	RemoveRecent(paths[10])
	if Recent()[0] == paths[10] {
		t.Error("RemoveRecent kept path")
	}
}

func TestProjectsDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)
	dir, err := ProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(base, "framestudio", "projects") {
		t.Errorf("dir = %s", dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Error("dir not created")
	}
}

func TestSuggestPathDedup(t *testing.T) {
	dir := t.TempDir()
	p1 := SuggestPath(dir, "Mein Film: Teil 1/2")
	if filepath.Base(p1) != "Mein Film- Teil 1-2.fsproj" {
		t.Errorf("p1 = %s", p1)
	}
	_ = os.WriteFile(p1, nil, 0o644)
	p2 := SuggestPath(dir, "Mein Film: Teil 1/2")
	if filepath.Base(p2) != "Mein Film- Teil 1-2 2.fsproj" {
		t.Errorf("p2 = %s", p2)
	}
	_ = os.WriteFile(p2, nil, 0o644)
	if p3 := SuggestPath(dir, "Mein Film: Teil 1/2"); filepath.Base(p3) != "Mein Film- Teil 1-2 3.fsproj" {
		t.Errorf("p3 = %s", p3)
	}
	if p := SuggestPath(dir, "  ..  "); filepath.Base(p) != "Unbenanntes Projekt.fsproj" {
		t.Errorf("empty name = %s", p)
	}
	if p := SuggestPath(dir, "../../etc/passwd"); filepath.Dir(p) != dir {
		t.Errorf("escaped dir: %s", p)
	}
}

func TestSortAndFilterDetails(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	mk := func(name, file string, ago time.Duration) Details {
		return Details{Entry: Entry{Name: name, Path: "/lib/" + file, Modified: now.Add(-ago)}}
	}
	ds := []Details{
		mk("Zebra", "zebra.fsproj", 3*time.Hour),
		mk("über Uns", "ueber.fsproj", time.Hour),
		mk("alpha", "alpha.fsproj", 2*time.Hour),
		mk("Umbau", "umbau.fsproj", 4*time.Hour),
	}
	names := func(ds []Details) string {
		var n []string
		for _, d := range ds {
			n = append(n, d.Name)
		}
		return strings.Join(n, ",")
	}
	SortDetails(ds, SortName)
	if got := names(ds); got != "alpha,über Uns,Umbau,Zebra" {
		t.Errorf("by name: %s", got)
	}
	SortDetails(ds, SortModified)
	if got := names(ds); got != "über Uns,alpha,Zebra,Umbau" {
		t.Errorf("by date: %s", got)
	}
	if got := names(FilterDetails(ds, "  U ")); got != "über Uns,Umbau" {
		t.Errorf("filter ub: %s", got)
	}
	if got := names(FilterDetails(ds, "ueber")); got != "über Uns" {
		t.Errorf("filter by file name: %s", got)
	}
	if got := names(FilterDetails(ds, "uns zeb")); got != "" {
		t.Errorf("all words must match: %s", got)
	}
	if got := FilterDetails(ds, ""); len(got) != 4 {
		t.Errorf("empty query keeps all: %d", len(got))
	}
}

func TestRationalRateRoundTripAndOldFiles(t *testing.T) {
	dir := t.TempDir()
	p := sample("ntsc")
	p.Settings.FPS, p.Settings.DropFrame = model.NTSC30, true
	path := filepath.Join(dir, "n.fsproj")
	if err := Save(p, path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"version": 2`) || !strings.Contains(string(raw), `"num": 30000`) {
		t.Errorf("file format: %s", raw)
	}
	q, err := Load(path)
	if err != nil || q.Settings.FPS != model.NTSC30 || !q.Settings.DropFrame {
		t.Fatalf("round trip: %+v %v", q.Settings, err)
	}
	// integer rates stay plain numbers, so version 1 readers could still read them
	p.Settings.FPS, p.Settings.DropFrame = model.Rate25, false
	_ = Save(p, path)
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), `"fps": 25`) {
		t.Errorf("integer rate: %s", raw)
	}
	// a version 1 file with a float media rate loads unchanged
	old := filepath.Join(dir, "old.fsproj")
	_ = os.WriteFile(old, []byte(`{"version":1,"name":"A","settings":{"width":640,"height":360,"fps":30},"media":[{"id":"m","name":"v","path":"/x","kind":"video","fps":29.97}],"tracks":[]}`), 0o644)
	o, err := Load(old)
	if err != nil || o.Settings.FPS != model.Rate30 || o.Media[0].FPS != model.NTSC30 {
		t.Fatalf("old file: %+v %v", o, err)
	}
}
