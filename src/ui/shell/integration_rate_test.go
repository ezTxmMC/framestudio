package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"framestudio-proto/src/export"
	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/store"
)

// New project at 29.97 drop-frame through the dialog, generated media, edit, rate change
// in the project settings (one undo), ffmpeg export checked with ffprobe, save and load.
func TestRateIntegrationFlow(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	sh, w, _ := newTestShell(t)
	sh.newProjectDialog()
	n := sh.npd
	n.name.SetText("Rate")
	n.picker.Res.Width.SetText("640")
	n.picker.Res.Height.SetText("360")
	n.picker.Rate.Select().SetSelected(model.NTSC30.Label())
	n.picker.Rate.SetDropFrame(true)
	n.submit()
	sess := sh.Active()
	st := sess.Project.Settings
	if st.Width != 640 || st.Height != 360 || !st.FPS.Equal(model.NTSC30) || !st.DropFrame {
		t.Fatalf("settings %+v", st)
	}
	if tc := sess.Project.Timecode(1800); tc != "00:01:00;02" {
		t.Errorf("drop-frame timecode of frame 1800: %q", tc)
	}

	src := filepath.Join(t.TempDir(), "src.mp4")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-y", "-f", "lavfi", "-i",
		"testsrc=size=320x180:rate=30000/1001:duration=3", "-pix_fmt", "yuv420p", src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	m, err := media.Probe(src)
	if err != nil {
		t.Fatal(err)
	}
	if !m.FPS.Equal(model.NTSC30) {
		t.Fatalf("probed %v", m.FPS)
	}
	if err := sess.Edit("Medium", func(p *model.Project) error {
		p.AddMedia(m)
		c := p.ClipFromMedia(m, 0, 0)
		return p.PlaceClip(p.Tracks[0].ID, c)
	}); err != nil {
		t.Fatal(err)
	}
	edit(t, sh)
	frames := sess.Project.Duration()

	// rate change in project settings: one undo restores
	before, _ := json.Marshal(sess.Project.Settings)
	sh.projectSettings()
	u := sh.psd
	u.picker.Rate.Select().SetSelected(model.Rate25.Label())
	u.Apply()
	if !sess.Project.Rate().Equal(model.Rate25) || sess.Project.DropFrame() {
		t.Fatalf("not applied: %+v", sess.Project.Settings)
	}
	sess.Undo()
	after, _ := json.Marshal(sess.Project.Settings)
	if string(before) != string(after) || sess.Project.Duration() != frames {
		t.Fatalf("undo: %s vs %s, duration %d vs %d", before, after, sess.Project.Duration(), frames)
	}
	closeTop(w)

	// export
	out := filepath.Join(t.TempDir(), "o.mp4")
	r := render.New(media.NewCache())
	o := export.Options{Format: export.FormatMP4, Path: out, Width: 640, Height: 360, Quality: export.QualityLow}
	if err := export.Run(context.Background(), sess.Project, r, o, nil); err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=r_frame_rate,nb_frames", "-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); !strings.HasPrefix(got, "30000/1001,") || !strings.HasSuffix(got, fmt.Sprint(frames)) {
		t.Errorf("ffprobe %q, want 30000/1001 and %d frames", got, frames)
	}

	// save / load round trip
	path := filepath.Join(t.TempDir(), "rate.fsproj")
	if err := store.Save(sess.Project, path); err != nil {
		t.Fatal(err)
	}
	q, err := store.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Rate().Equal(model.NTSC30) || !q.DropFrame() || q.Duration() != frames || q.Version != model.FormatVersion {
		t.Errorf("round trip: %+v", q.Settings)
	}
	_ = settings.Get()
}

// A format 1 file (integer fps, no rate fields) loads unchanged.
func TestLegacyV1ProjectFixture(t *testing.T) {
	p := model.NewProject("Alt")
	if err := p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Farbe", 10, 40)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["version"] = 1
	s := m["settings"].(map[string]any)
	s["fps"] = 30
	delete(s, "dropFrame")
	raw, _ = json.MarshalIndent(m, "", " ")
	if !strings.Contains(string(raw), `"fps": 30`) {
		t.Fatal("fixture must hold an integer fps")
	}
	path := filepath.Join(t.TempDir(), "alt.fsproj")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	q, err := store.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Rate().Equal(model.Rate30) || q.DropFrame() || q.Duration() != 50 || q.Settings.Width != 1920 {
		t.Errorf("legacy load: %+v duration %d", q.Settings, q.Duration())
	}
}
