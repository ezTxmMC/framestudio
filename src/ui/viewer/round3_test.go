package viewer

import (
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/settings"
)

func withSettings(t *testing.T, fn func(s *settings.Settings)) {
	t.Helper()
	_ = settings.Update(fn)
	t.Cleanup(func() { _ = settings.Update(func(s *settings.Settings) { *s = settings.Defaults() }) })
}

func TestPreviewQualityFollowsSettings(t *testing.T) {
	s, v, src, q, _ := setup(t)
	shown := func() int {
		if img := v.Image(); img != nil {
			return img.Bounds().Dx()
		}
		return 0
	}
	q.until(t, "full size", func() bool { return shown() > 0 && shown() == v.target().W })
	full := shown()
	if v.Quality() != QualityAuto {
		t.Fatalf("default quality %q", v.Quality())
	}
	withSettings(t, func(st *settings.Settings) { st.Playback.PreviewQuality = QualityHalf })
	q.until(t, "half", func() bool { return shown() < full*3/4 })
	if w := shown(); w > full/2+1 || w < full/2-1 {
		t.Fatalf("half quality rendered %d of %d px", w, full)
	}
	withSettings(t, func(st *settings.Settings) { st.Playback.PreviewQuality = QualityQuarter })
	q.until(t, "quarter", func() bool { return shown() < full*3/8 })
	if w := shown(); w > full/4+1 {
		t.Fatalf("quarter quality rendered %d of %d px", w, full)
	}
	// a viewer may override the setting
	v.SetQuality(QualityFull)
	q.until(t, "full", func() bool { return shown() == full })
	v.SetQuality("")
	withSettings(t, func(st *settings.Settings) {
		st.Playback.PreviewQuality = QualityAuto
		st.Playback.MaxPreviewH = 120
	})
	s.SetPlaying(true)
	q.until(t, "capped", func() bool { return src.last().h <= 120 })
	if h := src.last().h; h > 120 {
		t.Fatalf("playing preview should stop at MaxPreviewH: %d", h)
	}
	s.SetPlaying(false)
}

func TestSafeAreaGuides(t *testing.T) {
	_, v, _, q, w := setup(t)
	q.wait(t)
	if v.SafeAreaShown() {
		t.Fatal("guides are off by default")
	}
	v.ShowSafeArea(true)
	pic := v.pic.Size()
	a, ti := v.safe.action, v.safe.title
	if !v.SafeAreaShown() || a.Size().Width < pic.Width*0.92 || a.Size().Width > pic.Width*0.94 ||
		ti.Size().Height < pic.Height*0.89 || ti.Size().Height > pic.Height*0.91 {
		t.Fatalf("guides: action %v title %v picture %v", a.Size(), ti.Size(), pic)
	}
	if os.Getenv("FS_SCREENSHOT") != "" {
		w.Resize(fyne.NewSize(960, 600))
		f, err := os.Create("/tmp/fs-r3-edit-safearea.png")
		if err == nil {
			_ = png.Encode(f, w.Canvas().Capture())
			f.Close()
		}
	}
	v.ShowSafeArea(false)
	if v.SafeAreaShown() {
		t.Fatal("guides should hide")
	}
}
