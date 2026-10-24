package command

import (
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"fyne.io/fyne/v2"
	"math"
	"testing"
)

func TestKeyboardPresets(t *testing.T) {
	for _, preset := range KeyPresets {
		km, ok := PresetKeymap(preset.ID)
		if !ok {
			t.Fatal(preset.ID)
		}
		reg := NewRegistry()
		reg.SetKeymap(km)
		used := map[string]string{}
		for _, in := range Catalog {
			sc := Normalize(reg.Shortcut(in.ID))
			if sc == "" {
				continue
			}
			if prev := used[sc]; prev != "" {
				t.Errorf("%s: %s shared by %s and %s", preset.Name, sc, prev, in.ID)
			}
			used[sc] = in.ID
		}
	}
	reg := NewRegistry()
	if reg.Shortcut("clip.split") != "Ctrl+K" || reg.Shortcut("edit.rippleDelete") != "Delete" || reg.Shortcut("edit.delete") != "Backspace" {
		t.Fatal("Frame Studio defaults")
	}
	var ran string
	reg.Bind(Global, Binding{ID: "clip.split", Run: func() { ran = "split" }}, Binding{ID: "edit.rippleDelete", Run: func() { ran = "ripple" }}, Binding{ID: "edit.delete", Run: func() { ran = "delete" }})
	for _, key := range []struct {
		k    fyne.KeyName
		m    fyne.KeyModifier
		want string
	}{{fyne.KeyK, fyne.KeyModifierControl, "split"}, {fyne.KeyDelete, 0, "ripple"}, {fyne.KeyBackspace, 0, "delete"}} {
		if !reg.Dispatch(key.k, key.m) || ran != key.want {
			t.Fatalf("key %s ran %s", key.k, ran)
		}
	}
}
func TestGainHotkeysExceed12DBAndUndo(t *testing.T) {
	p := model.NewProject("gain")
	clip := model.NewClip(model.KindAudio, "audio", 0, 25)
	clip.Volume = math.Pow(10, 24.0/20)
	_ = p.PlaceClip(p.Tracks[2].ID, clip)
	s := session.New(p)
	s.SelectClip(clip.ID)
	reg := NewRegistry()
	reg.Bind(Global, AudioBindings(s, func(err error) { t.Fatal(err) })...)
	if !reg.Dispatch(fyne.KeyUp, fyne.KeyModifierAlt) {
		t.Fatal("gain shortcut")
	}
	c, _, _ := s.Project.Clip(clip.ID)
	if math.Abs(20*math.Log10(c.Volume)-25) > 1e-9 {
		t.Fatal(c.Volume)
	}
	s.Undo()
	c, _, _ = s.Project.Clip(clip.ID)
	if c.Volume != clip.Volume {
		t.Fatal("gain undo")
	}
	reg.Run("audio.trackGainUp")
	track, _ := s.Project.Track(s.SelectedTrack)
	if track.Volume <= 1 {
		t.Fatal("track gain")
	}
	reg.Run("audio.trackMute")
	if !track.Muted {
		t.Fatal("track mute")
	}
}
