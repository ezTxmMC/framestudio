package media

import (
	"strings"
	"testing"
	"time"

	"framestudio-proto/src/model"
)

func TestUseAsProjectFormat(t *testing.T) {
	h := newHarness(t)
	p := h.s.Project
	p.AddMedia(model.MediaItem{Name: "Handy.mp4", Kind: model.KindVideo, Width: 1080, Height: 1920, FPS: model.NTSC30,
		Duration: 4 * time.Second, Path: "/x/handy.mp4"})
	p.AddMedia(model.MediaItem{Name: "Foto.png", Kind: model.KindImage, Width: 2000, Height: 1500, Path: "/x/foto.png"})
	p.AddMedia(model.MediaItem{Name: "Ton.wav", Kind: model.KindAudio, Duration: time.Second, Path: "/x/ton.wav"})
	_ = p.PlaceClip(p.Tracks[0].ID, p.ClipFromMedia(p.Media[0], 0, 0))

	var gotMsg string
	var ask bool
	h.p.formatDialog = func(_, msg string, a bool, yes func()) {
		gotMsg, ask = msg, a
		if a {
			yes()
		}
	}

	h.p.useAsProjectFormat(p.Media[2]) // audio: refused
	if len(h.errors) != 1 || !strings.Contains(h.errors[0].Error(), "keine Bildgröße") {
		t.Fatalf("audio must be refused with a reason: %v", h.errors)
	}

	before := len(h.s.Project.Tracks[0].Clips)
	h.p.useAsProjectFormat(p.Media[0])
	st := h.s.Project.Settings
	if !ask || st.Width != 1080 || st.Height != 1920 || !st.FPS.Equal(model.NTSC30) {
		t.Fatalf("settings %+v, asked %v", st, ask)
	}
	if !strings.Contains(gotMsg, "1080 × 1920, 29,97 fps") || !strings.Contains(gotMsg, "bisher 1920 × 1080, 25 fps") {
		t.Errorf("confirm text %q", gotMsg)
	}
	if len(h.s.Project.Tracks[0].Clips) != before {
		t.Error("clips must stay")
	}
	h.s.Undo()
	if h.s.Project.Settings.Width != 1920 || !h.s.Project.Rate().Equal(model.Rate25) || h.s.CanUndo() {
		t.Fatalf("one undo step must restore size and rate: %+v", h.s.Project.Settings)
	}

	// an image changes the size only
	h.p.useAsProjectFormat(p.Media[1])
	if st := h.s.Project.Settings; st.Width != 2000 || st.Height != 1500 || !st.FPS.Equal(model.Rate25) {
		t.Fatalf("image format: %+v", st)
	}
	// already the project format: only a message
	h.p.useAsProjectFormat(p.Media[1])
	if ask || !strings.Contains(gotMsg, "schon das Format") {
		t.Errorf("same format: ask=%v %q", ask, gotMsg)
	}
}
