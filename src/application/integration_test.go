package application

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/audio"
	"framestudio-proto/src/model"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/liveaudio"
	"framestudio-proto/src/ui/shell"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/timeline"
	"framestudio-proto/src/ui/viewer"
)

type nullOutput struct{}

func (nullOutput) Write(b []byte) (int, error) {
	time.Sleep(time.Duration(len(b)/4) * time.Second / audio.SampleRate)
	return len(b), nil
}
func (nullOutput) Latency() time.Duration { return 0 }
func (nullOutput) Close() error           { return nil }

// find returns the first visible object of type T in w.
func find[T fyne.CanvasObject](w fyne.Window) (T, bool) {
	var found T
	ok := false
	var walk func(o fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		if ok || o == nil || !o.Visible() {
			return
		}
		if x, is := o.(T); is {
			found, ok = x, true
			return
		}
		switch x := o.(type) {
		case *fyne.Container:
			for _, c := range x.Objects {
				walk(c)
			}
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(x).Objects() {
				walk(c)
			}
		}
	}
	walk(w.Canvas().Content())
	return found, ok
}

// TestSettingsApplyLiveInTheApp runs the real shell with Follow and changes settings
// through the menu commands and settings.Update: accent colour, playhead follow mode,
// preview quality and audio mute reach the theme, the timeline, the viewer and the live
// audio engine at once.
func TestSettingsApplyLiveInTheApp(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := settings.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = settings.Reset(); studiotheme.Configure("steel", false) })
	var engines []*audio.Engine
	t.Cleanup(liveaudio.UseEngine(func() *audio.Engine {
		e := audio.New(audio.Config{Open: func() (audio.Output, error) { return nullOutput{}, nil }})
		engines = append(engines, e)
		return e
	}))
	a := test.NewTempApp(t)
	q := kit.UseUIQueue(t)
	a.Settings().SetTheme(studiotheme.New())
	stop := Follow()
	defer stop()

	p := model.NewProject("Live")
	if err := p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Farbe", 0, 100)); err != nil {
		t.Fatal(err)
	}
	dir, err := store.ProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	path := store.SuggestPath(dir, p.Name)
	if err := store.Save(p, path); err != nil {
		t.Fatal(err)
	}
	w := a.NewWindow("Frame Studio")
	w.Resize(fyne.NewSize(1440, 900))
	sh := shell.New(a, w)
	t.Cleanup(func() {
		sh.Close()
		q.Settle(50*time.Millisecond, 2*time.Second)
	})
	sh.Start([]string{path})
	reg := sh.Commands()
	if !reg.Run("view.page.edit") {
		t.Fatal("edit page did not open")
	}
	q.Settle(50*time.Millisecond, 5*time.Second)

	// Accent colour.
	_ = settings.Update(func(s *settings.Settings) { s.Display.Accent = "teal" })
	if studiotheme.Steel != studiotheme.AccentColor("teal") {
		t.Fatal("accent colour not applied")
	}

	// Follow mode through the menu command.
	tl, ok := find[*timeline.Timeline](w)
	if !ok {
		t.Fatal("no timeline")
	}
	if !reg.Run("timeline.follow.smooth") || tl.Follow() != timeline.FollowSmooth {
		t.Fatalf("follow mode %q, want smooth", tl.Follow())
	}
	if c, _ := reg.Get("timeline.follow.smooth"); !c.Checked {
		t.Fatal("menu check mark does not follow the setting")
	}

	// Preview quality through the menu command.
	v, ok := find[*viewer.Viewer](w)
	if !ok {
		t.Fatal("no viewer")
	}
	if !reg.Run("play.quality.half") || v.Quality() != viewer.QualityHalf {
		t.Fatalf("preview quality %q, want half", v.Quality())
	}

	// Audio mute through the menu command reaches the running engine.
	reg.Run("play.toggle")
	q.Settle(50*time.Millisecond, 2*time.Second)
	reg.Run("play.toggle")
	if !reg.Run("play.mute") || !settings.Get().Audio.Muted || !liveaudio.CurrentConfig().Muted {
		t.Fatal("mute not applied to the live audio configuration")
	}
	if len(engines) == 0 {
		t.Fatal("no live audio engine was started")
	}
	if !engines[len(engines)-1].Muted() {
		t.Fatal("running engine not muted")
	}
	reg.Run("play.mute")
	if engines[len(engines)-1].Muted() {
		t.Fatal("engine stays muted after unmuting")
	}
}
