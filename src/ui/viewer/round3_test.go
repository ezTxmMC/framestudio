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
	q.wait(t)
	full := src.last()
	if v.Quality() != QualityAuto {
		t.Fatalf("default quality %q", v.Quality())
	}
	withSettings(t, func(st *settings.Settings) { st.Playback.PreviewQuality = QualityHalf })
	q.wait(t)
	if c := src.last(); c.w > full.w/2+1 || c.w < full.w/2-1 {
		t.Fatalf("half quality rendered %d of %d px", c.w, full.w)
	}
	withSettings(t, func(st *settings.Settings) { st.Playback.PreviewQuality = QualityQuarter })
	q.wait(t)
	if c := src.last(); c.w > full.w/4+1 {
		t.Fatalf("quarter quality rendered %d of %d px", c.w, full.w)
	}
	// a viewer may override the setting
	v.SetQuality(QualityFull)
	q.wait(t)
	if src.last().w != full.w {
		t.Fatal("full override should render at full size")
	}
	v.SetQuality("")
	withSettings(t, func(st *settings.Settings) {
		st.Playback.PreviewQuality = QualityAuto
		st.Playback.MaxPreviewH = 120
	})
	q.wait(t)
	s.SetPlaying(true)
	q.wait(t)
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
