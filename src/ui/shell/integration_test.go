package shell

import (
	"context"
	"encoding/json"
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
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/export"
	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

// demoMedia generates a still, and (with ffmpeg) a test-pattern video with sound and a sine tone.
func demoMedia(t *testing.T, dir string) (still, video, tone string) {
	t.Helper()
	still = filepath.Join(dir, "Logo.png")
	img := image.NewNRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(40 + x/4), uint8(60 + y/3), 150, 255})
		}
	}
	f, err := os.Create(still)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return still, "", ""
	}
	video = filepath.Join(dir, "Testbild.mp4")
	tone = filepath.Join(dir, "Musik.wav")
	run := func(args ...string) {
		if out, err := exec.Command("ffmpeg", append([]string{"-v", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, out)
		}
	}
	run("-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=8",
		"-f", "lavfi", "-i", "sine=frequency=660:duration=8",
		"-af", "volume=4", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", video)
	run("-f", "lavfi", "-i", "sine=frequency=220:duration=14", "-af", "volume=5", tone)
	return still, video, tone
}

// demoProject builds a 1080p25 project: V1 colour, video, image; V2 title; A1 music, A2 video sound.
func demoProject(t *testing.T, still, video, tone string) *model.Project {
	t.Helper()
	p := model.NewProject("Integration Demo")
	v1, v2, a1 := p.Tracks[0].ID, p.Tracks[1].ID, p.Tracks[2].ID
	a2 := p.AddTrack(model.TrackAudio).ID
	place := func(track string, c model.Clip) {
		if err := p.PlaceClip(track, c); err != nil {
			t.Fatal(err)
		}
	}
	add := func(path string) *model.MediaItem {
		m, err := media.Probe(path)
		if err != nil {
			t.Fatal(err)
		}
		item, _ := p.MediaByID(p.AddMedia(m))
		return item
	}

	bg := model.NewClip(model.KindColor, "Farbfläche", 0, 50)
	bg.Color = model.RGBA{R: 0x2E, G: 0x4A, B: 0x6B, A: 0xFF}
	bg.FadeIn = 12
	place(v1, bg)
	at := 50
	if video != "" {
		m := add(video)
		c := p.ClipFromMedia(*m, at, 125)
		c.Length = 150
		c.Grade = model.Grade{Contrast: 0.15, Saturation: 0.2, Vignette: 0.3}
		place(v1, c)
		snd := p.ClipFromMedia(*m, at, 125)
		snd.Kind, snd.Length = model.KindAudio, 150
		place(a2, snd)
		at += 150
	}
	img := p.ClipFromMedia(*add(still), at, 100)
	img.Length = 100
	place(v1, img)

	title := model.NewClip(model.KindTitle, "Bauchbinde", 75, 100)
	title.Title = model.Title{Text: "Frame Studio\nIntegration", Size: 0.07, Color: model.RGBA{R: 255, G: 255, B: 255, A: 255},
		Bold: true, Align: model.AlignLeft, Y: 0.8, Background: model.RGBA{A: 150}}
	place(v2, title)

	if tone != "" {
		m := add(tone)
		c := p.ClipFromMedia(*m, 0, 300)
		c.Length = 300
		c.FadeIn, c.FadeOut, c.Volume = 25, 50, 0.7
		place(a1, c)
	}
	p.AddMarker(50, "Start Interview")
	p.AddMarker(200, "Logo")
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	return p
}

// newIntegrationShell builds the real shell with the real pages.
func newIntegrationShell(t *testing.T) (*Shell, fyne.Window, *kit.UIQueue) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := test.NewTempApp(t)
	q := kit.UseUIQueue(t)
	a.Settings().SetTheme(studiotheme.New())
	w := test.NewTempWindow(t, nil)
	w.Resize(fyne.NewSize(1440, 900))
	sh := New(a, w)
	sh.closeWindow = func() {}
	t.Cleanup(func() {
		sh.shutdown()
		q.Settle(50*time.Millisecond, 2*time.Second)
		sh.cache.Close()
	})
	return sh, w, q
}

func TestIntegrationPages(t *testing.T) {
	sh, w, q := newIntegrationShell(t)
	shots := os.Getenv("FS_SCREENSHOT") != ""
	mediaDir := t.TempDir()
	if shots { // keep media and a project copy for trying the real app
		mediaDir = "/tmp/fs-int-media"
		if err := os.MkdirAll(mediaDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	still, video, tone := demoMedia(t, mediaDir)
	if video == "" {
		t.Log("ffmpeg fehlt: Demo ohne Video und Ton")
	}
	proj := demoProject(t, still, video, tone)
	dir, err := store.ProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	path := store.SuggestPath(dir, proj.Name)
	if err := store.Save(proj, path); err != nil {
		t.Fatal(err)
	}
	if shots {
		if err := store.Save(proj, filepath.Join(mediaDir, "Integration Demo.fsproj")); err != nil {
			t.Fatal(err)
		}
	}
	sh.thumbnails = true
	sh.Start([]string{path})
	s := sh.Active()
	if s == nil {
		t.Fatal("project did not open")
	}
	s.SetIn(25)
	s.SetOut(225)
	s.Seek(110)

	pages := []struct{ id, name string }{
		{page.Media, "medien"}, {page.Edit, "schnitt"}, {page.Color, "farbe"},
		{page.Audio, "ton"}, {page.Deliver, "export"},
	}
	for _, pg := range pages {
		sh.showPageID(pg.id)
		if sh.active.current != pg.id {
			t.Fatalf("page %s not shown", pg.id)
		}
		if pg.id == page.Media && len(s.Project.Media) > 0 {
			s.SelectMedia(s.Project.Media[0].ID)
		}
		if pg.id == page.Color {
			if c, ok := firstClip(s.Project, model.KindVideo); ok {
				s.SelectClip(c)
			}
		}
		if pg.id == page.Audio {
			if c, ok := firstClip(s.Project, model.KindAudio); ok {
				s.SelectClip(c)
			}
		}
		q.Settle(400*time.Millisecond, 15*time.Second)
		img := w.Canvas().Capture()
		if shots {
			writePNG(t, img, "/tmp/fs-int-"+pg.name+".png")
		}
	}

	sh.ShowManager()
	q.Settle(400*time.Millisecond, 10*time.Second)
	if shots {
		writePNG(t, w.Canvas().Capture(), "/tmp/fs-int-manager.png")
	}
	if !sh.showManager {
		t.Fatal("manager not shown")
	}
}

func firstClip(p *model.Project, kind model.ClipKind) (string, bool) {
	for _, tr := range p.Tracks {
		for _, c := range tr.Clips {
			if c.Kind == kind {
				return c.ID, true
			}
		}
	}
	return "", false
}

func writePNG(t *testing.T, img image.Image, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationExport(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffmpeg/ffprobe fehlt")
	}
	still, video, tone := demoMedia(t, t.TempDir())
	p := demoProject(t, still, video, tone)
	cache := media.NewCache()
	defer cache.Close()
	out := filepath.Join(t.TempDir(), "demo.mp4")
	o := export.Options{Format: export.FormatMP4, Path: out, Width: 160, Height: 90,
		Quality: export.QualityMedium, From: 40, To: 90, IncludeAudio: true}
	if err := export.Validate(p, o); err != nil {
		t.Fatal(err)
	}
	if err := export.Run(context.Background(), p, nil, o, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-show_streams", "-show_format", "-of", "json", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Frames    string `json:"nb_read_frames"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, st := range info.Streams {
		kinds = append(kinds, st.CodecType)
		if st.CodecType == "video" && (st.Width != 160 || st.Height != 90 || st.Frames != "50") {
			t.Errorf("video stream %dx%d, %s frames; want 160x90, 50", st.Width, st.Height, st.Frames)
		}
	}
	if strings.Join(kinds, ",") != "video,audio" {
		t.Errorf("streams %v, want video and audio", kinds)
	}
	if !strings.HasPrefix(info.Format.Duration, "2.0") && !strings.HasPrefix(info.Format.Duration, "1.9") {
		t.Errorf("duration %s, want about 2 s", info.Format.Duration)
	}
}
