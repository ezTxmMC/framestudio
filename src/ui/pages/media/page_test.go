package media

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

type harness struct {
	p      *Page
	s      *session.Session
	w      fyne.Window
	queue  []func()
	errors []error
}

// newHarness builds the page in a 1440x860 test window. Background work is queued
// and run by drain on the test goroutine.
func newHarness(t *testing.T) *harness {
	t.Helper()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	h := &harness{s: session.New(model.NewProject("Test"))}
	w := test.NewTempWindow(t, nil)
	h.w = w
	cache := media.NewCache()
	t.Cleanup(cache.Close)
	ctx := &page.Context{Window: w, Session: h.s, Media: cache,
		Navigate: func(string) {}, ShowErr: func(err error) { h.errors = append(h.errors, err) }}
	h.p = New(ctx).(*Page)
	h.p.spawn = func(f func()) { h.queue = append(h.queue, f) }
	h.p.files.open(t.TempDir())
	w.SetContent(h.p.Content())
	w.Resize(fyne.NewSize(1440, 860))
	t.Cleanup(h.p.Close)
	return h
}

func (h *harness) drain() {
	for i := 0; i < 20 && len(h.queue) > 0; i++ {
		q := h.queue
		h.queue = nil
		for _, f := range q {
			f()
		}
		h.w.Canvas().Refresh(h.w.Content())
	}
}

func writePNG(t *testing.T, dir, name string, w, h int, c color.NRGBA) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			k := float64(x+y) / float64(w+h)
			img.SetNRGBA(x, y, color.NRGBA{uint8(float64(c.R) * (0.4 + k)), uint8(float64(c.G) * (0.4 + k)), uint8(float64(c.B) * (0.4 + k)), 255})
		}
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func ffmpegFile(t *testing.T, path string, args ...string) string {
	t.Helper()
	if !media.FFmpegAvailable() {
		t.Skip("ffmpeg nicht installiert")
	}
	all := append([]string{"-nostdin", "-loglevel", "error", "-y"}, args...)
	if out, err := exec.Command("ffmpeg", append(all, path)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	return path
}

func testVideo(t *testing.T, dir, name, src string) string {
	return ffmpegFile(t, filepath.Join(dir, name), "-f", "lavfi", "-i", src,
		"-f", "lavfi", "-i", "sine=frequency=330:duration=4", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
}

func TestPlaceMedia(t *testing.T) {
	p := model.NewProject("x")
	v1, a1 := p.Tracks[0].ID, p.Tracks[2].ID
	vid := model.MediaItem{ID: "v", Name: "Interview", Kind: model.KindVideo, Duration: 2 * time.Second, HasAudio: true, Width: 1920, Height: 1080}
	id, end, err := placeMedia(p, vid, 10, a1) // audio track selected: falls back to V1
	if err != nil {
		t.Fatal(err)
	}
	c, ti, _ := p.Clip(id)
	if c == nil || p.Tracks[ti].ID != v1 || c.Start != 10 || c.Length != 50 || end != 60 {
		t.Fatalf("video clip %+v on track %d", c, ti)
	}
	if a := p.Tracks[2].Clips; len(a) != 1 || a[0].Kind != model.KindAudio || a[0].MediaID != "v" || a[0].Start != 10 || a[0].Length != 50 {
		t.Fatalf("audio clip %+v", a)
	}

	still := model.MediaItem{ID: "s", Name: "Logo", Kind: model.KindImage, Width: 10, Height: 10}
	v2 := p.Tracks[1].ID
	id, _, _ = placeMedia(p, still, 0, v2)
	if c, ti, _ := p.Clip(id); p.Tracks[ti].ID != v2 || c.Length != 5*25 {
		t.Fatalf("still clip %+v on %d", c, ti)
	}

	p.Tracks[0].Locked = true
	p.Tracks[1].Locked = true
	if _, _, err := placeMedia(p, still, 0, v1); err == nil || !strings.Contains(err.Error(), "gesperrt") {
		t.Fatalf("locked tracks should fail, got %v", err)
	}

	q := model.NewProject("y")
	_ = q.RemoveTrack(q.Tracks[2].ID)
	if _, _, err := placeMedia(q, model.MediaItem{ID: "a", Kind: model.KindAudio, Duration: time.Second}, 0, ""); err != nil {
		t.Fatal(err)
	}
	if len(q.TrackIndices(model.TrackAudio)) != 1 {
		t.Fatal("a missing audio track should be created")
	}
	if usage(p, "v") != 2 || usage(p, "none") != 0 {
		t.Fatal("usage count wrong")
	}
}

func TestPlanImport(t *testing.T) {
	dir := t.TempDir()
	a := writePNG(t, dir, "b.png", 4, 4, color.NRGBA{200, 0, 0, 255})
	writePNG(t, dir, "A.jpg.png", 4, 4, color.NRGBA{0, 200, 0, 255})
	_ = os.WriteFile(filepath.Join(dir, "notiz.txt"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".versteckt.png"), []byte("x"), 0o644)
	other := filepath.Join(t.TempDir(), "doku.pdf")
	plan := planImport([]string{dir, a, other}, map[string]bool{})
	if len(plan.files) != 2 || filepath.Base(plan.files[0]) != "A.jpg.png" {
		t.Fatalf("files %v", plan.files)
	}
	if len(plan.unsupported) != 1 || plan.unsupported[0] != "doku.pdf" {
		t.Fatalf("unsupported %v", plan.unsupported)
	}
	plan = planImport([]string{a}, map[string]bool{a: true})
	if len(plan.files) != 0 || plan.duplicates != 1 {
		t.Fatalf("duplicate not detected: %+v", plan)
	}
	if s := importSummary(importPlan{unsupported: []string{"x.doc"}}, []string{"„y.mp4“ kaputt"}); !strings.Contains(s, "x.doc") || !strings.Contains(s, "kaputt") {
		t.Fatalf("summary %q", s)
	}
}

func TestImportInsertRemove(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	a := writePNG(t, dir, "Titelbild.png", 64, 36, color.NRGBA{90, 140, 230, 255})
	writePNG(t, dir, "Abspann.png", 64, 36, color.NRGBA{200, 120, 60, 255})
	_ = os.WriteFile(filepath.Join(dir, "liesmich.txt"), []byte("x"), 0o644)

	h.p.importPaths([]string{dir, filepath.Join(dir, "liesmich.txt")})
	if !h.p.importing || h.p.importBtn.Disabled() == false {
		t.Fatal("import should be running with buttons disabled")
	}
	h.drain()
	pr := h.s.Project
	if len(pr.Media) != 2 || h.s.UndoLabel() != "Medien importieren" {
		t.Fatalf("media %d, undo %q", len(pr.Media), h.s.UndoLabel())
	}
	if h.p.importing || h.s.SelectedMedia != pr.Media[0].ID || pr.Media[0].Name != "Abspann" {
		t.Fatalf("first imported item should be selected: %+v", pr.Media)
	}
	if len(h.p.view) != 2 || h.p.thumbs[pr.Media[0].ID+"|"+pr.Media[0].Path] == nil {
		t.Fatal("pool should show both items with thumbnails")
	}
	h.s.Undo()
	if len(h.s.Project.Media) != 0 {
		t.Fatal("import must be one undo step")
	}
	h.s.Redo()
	h.s.SelectMedia(h.s.Project.Media[0].ID)

	// Importing the same file again only selects nothing new.
	h.p.Dropped([]fyne.URI{storage.NewFileURI(a)})
	if len(h.s.Project.Media) != 2 || h.p.importing {
		t.Fatal("duplicate drop must not import")
	}

	// Enter inserts the selection at the playhead on the selected track.
	h.s.Seek(25)
	if !h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn}) {
		t.Fatal("Enter not handled")
	}
	c, ok := h.s.Clip()
	if !ok || c.Start != 25 || c.Length != 125 || c.MediaID != h.s.SelectedMedia || h.s.Playhead != 150 {
		t.Fatalf("inserted clip %+v, playhead %d", c, h.s.Playhead)
	}
	if h.p.insp.values["Verwendung"].Text != "1 Clip in der Timeline" {
		t.Fatalf("inspector usage %q", h.p.insp.values["Verwendung"].Text)
	}

	// Append goes behind the last clip.
	h.p.insp.appendBtn.OnTapped()
	if c, _ := h.s.Clip(); c.Start != 150 {
		t.Fatalf("append started at %d", c.Start)
	}

	// Delete asks first, then removes media and clips in one step.
	id := h.s.SelectedMedia
	if !h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDelete}) {
		t.Fatal("Delete not handled")
	}
	if len(h.s.Project.Media) != 2 {
		t.Fatal("Delete must confirm before removing")
	}
	h.p.remove(id)
	if _, ok := h.s.Project.MediaByID(id); ok || usage(h.s.Project, id) != 0 || h.s.UndoLabel() != "Medium entfernen" {
		t.Fatal("media or its clips still present")
	}
	if !h.p.insp.body.Hidden {
		t.Fatal("inspector should show the placeholder without selection")
	}
	if len(h.errors) != 0 {
		t.Fatalf("unexpected errors %v", h.errors)
	}
}

func TestFilterAndSelection(t *testing.T) {
	h := newHarness(t)
	_ = h.s.Edit("x", func(p *model.Project) error {
		p.AddMedia(model.MediaItem{ID: "1", Name: "Strand", Kind: model.KindImage, Path: "/nicht/da.png"})
		p.AddMedia(model.MediaItem{ID: "2", Name: "Stadt", Kind: model.KindVideo, Path: "/nicht/da.mp4"})
		p.AddMedia(model.MediaItem{ID: "3", Name: "Musik", Kind: model.KindAudio, Path: "/nicht/da.wav"})
		return nil
	})
	if len(h.p.view) != 3 || !h.p.missing["1"] {
		t.Fatal("pool should list all items and flag missing files")
	}
	h.p.search.SetText("st")
	if len(h.p.view) != 2 {
		t.Fatalf("search: %d items", len(h.p.view))
	}
	h.p.kindSel.SetSelected("Video")
	if len(h.p.view) != 1 || h.p.view[0].ID != "2" {
		t.Fatalf("kind filter: %+v", h.p.view)
	}
	h.p.search.SetText("")
	h.p.kindSel.SetSelectedIndex(0)

	cards := findCards(h.w.Content())
	if len(cards) != 3 {
		t.Fatalf("%d cards rendered", len(cards))
	}
	test.Tap(cards[1])
	if h.s.SelectedMedia != "2" || h.p.insp.name.Text != "Stadt" {
		t.Fatalf("tap selected %q", h.s.SelectedMedia)
	}
	h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	if h.s.SelectedMedia != "3" {
		t.Fatal("right arrow should select the next item")
	}
	if h.p.insp.status.Hidden {
		t.Fatal("missing file should be flagged in the inspector")
	}
	h.p.Close()
	h.s.SelectMedia("1")
	if h.p.insp.name.Text != "Musik" {
		t.Fatal("closed page must not react to session changes")
	}
}

func findCards(o fyne.CanvasObject) []*card {
	var out []*card
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case *card:
			out = append(out, v)
			return
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
			return
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(v).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return out
}

func TestRelink(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	old := writePNG(t, dir, "alt.png", 32, 18, color.NRGBA{100, 100, 100, 255})
	h.p.importPaths([]string{old})
	h.drain()
	id := h.s.SelectedMedia
	_ = os.Remove(old)
	h.p.refreshPool()
	if !h.p.missing[id] {
		t.Fatal("deleted file should be missing")
	}
	moved := writePNG(t, t.TempDir(), "neu.png", 64, 64, color.NRGBA{100, 100, 100, 255})
	h.p.relinkTo(id, moved)
	h.drain()
	m, _ := h.s.Project.MediaByID(id)
	if m.Path != moved || m.Width != 64 || m.Name != "alt" || h.p.missing[id] || h.s.UndoLabel() != "Medium neu verknüpfen" {
		t.Fatalf("relinked %+v", m)
	}
	if h.p.relinkTo(id, filepath.Join(dir, "x.wav")); true {
		h.drain()
	}
	if len(h.errors) != 1 {
		t.Fatalf("relink to a missing file should report one error, got %v", h.errors)
	}
	if err := relink(h.s.Project, id, model.MediaItem{Kind: model.KindVideo}); err == nil || !strings.Contains(err.Error(), "derselben Art") {
		t.Fatalf("kind mismatch: %v", err)
	}
}

func TestScreenshot(t *testing.T) {
	out := os.Getenv("FS_SCREENSHOT")
	if out == "" {
		t.Skip("FS_SCREENSHOT nicht gesetzt")
	}
	if !strings.HasSuffix(out, ".png") {
		out = "/tmp/fs-media.png"
	}
	h := newHarness(t)
	capture(t, h.w, strings.TrimSuffix(out, ".png")+"-empty.png")
	dir := t.TempDir()
	testVideo(t, dir, "Interview Totale.mp4", "testsrc2=size=640x360:rate=25:duration=4")
	testVideo(t, dir, "Drohnenflug Küste.mp4", "mandelbrot=size=640x360:rate=25")
	testVideo(t, dir, "Farbbalken.mp4", "smptehdbars=size=640x360:rate=25:duration=4")
	ffmpegFile(t, filepath.Join(dir, "Atmo Hafen.wav"), "-f", "lavfi", "-i", "anoisesrc=d=6:c=pink:a=0.4,volume=0.8")
	ffmpegFile(t, filepath.Join(dir, "Musikbett.mp3"), "-f", "lavfi", "-i", "sine=frequency=220:duration=8,tremolo=f=2:d=0.9")
	writePNG(t, dir, "Logo Bauchbinde.png", 1920, 1080, color.NRGBA{90, 140, 230, 255})
	writePNG(t, dir, "Standbild Abendlicht.png", 1280, 720, color.NRGBA{230, 140, 70, 255})
	gone := writePNG(t, dir, "Archivfoto.png", 800, 600, color.NRGBA{140, 140, 140, 255})
	h.p.importPaths([]string{dir})
	h.drain()
	_ = os.Remove(gone)
	_ = os.Mkdir(filepath.Join(dir, "Rohmaterial"), 0o755)
	h.p.files.open(dir)
	for _, m := range h.s.Project.Media {
		if m.Name == "Interview Totale" {
			h.s.SelectMedia(m.ID)
			h.p.insert(m, false)
			h.s.SelectMedia(m.ID)
		}
	}
	h.p.refreshPool()
	h.drain()
	h.drain()
	capture(t, h.w, out)
}

func capture(t *testing.T, w fyne.Window, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}
