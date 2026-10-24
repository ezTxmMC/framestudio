package command

import (
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"math"
)

// AudioBindings shares gain and track actions between the edit and audio workspaces.
func AudioBindings(s *session.Session, report func(error)) []Binding {
	clips := func() []string {
		var ids []string
		for _, id := range s.Selected {
			if c, _, _ := s.Project.Clip(id); c != nil && c.Kind == model.KindAudio {
				ids = append(ids, id)
			}
		}
		return ids
	}
	hasClips := func() bool { return len(clips()) > 0 }
	hasTrack := func() bool { t, _ := s.Project.Track(s.SelectedTrack); return t != nil && t.Kind == model.TrackAudio }
	apply := func(label, key string, fn func(*model.Project) error) {
		if err := s.EditCoalesced(key, label, fn); err != nil && report != nil {
			report(err)
		}
	}
	gain := func(v, delta float64, reset bool) float64 {
		if reset {
			return 1
		}
		db := -60.0
		if v > 0 {
			db = 20 * math.Log10(v)
		}
		db = math.Max(-60, math.Min(60, db+delta))
		if db <= -60 {
			return 0
		}
		return math.Pow(10, db/20)
	}
	var bindings []Binding
	for _, action := range []struct {
		id, label string
		delta     float64
		reset     bool
	}{
		{"GainUp", "Lautstärke +1 dB", 1, false}, {"GainDown", "Lautstärke −1 dB", -1, false}, {"GainReset", "Lautstärke auf 0 dB", 0, true},
	} {
		action := action
		bindings = append(bindings, Binding{ID: "audio.clip" + action.id, Enabled: hasClips, Run: func() {
			ids := clips()
			apply(action.label, "audio.clip"+action.id, func(p *model.Project) error {
				for _, id := range ids {
					c, _, _ := p.Clip(id)
					next := *c
					next.Volume = gain(c.Volume, action.delta, action.reset)
					if err := p.UpdateClip(next); err != nil {
						return err
					}
				}
				return nil
			})
		}},
			Binding{ID: "audio.track" + action.id, Enabled: hasTrack, Run: func() {
				id := s.SelectedTrack
				apply(action.label, "audio.track"+action.id, func(p *model.Project) error {
					t, _ := p.Track(id)
					if t == nil {
						return model.ErrNotFound
					}
					if t.Locked {
						return model.ErrLocked
					}
					t.Volume = gain(t.Volume, action.delta, action.reset)
					return nil
				})
			}})
	}
	for _, solo := range []bool{false, true} {
		id := "audio.trackMute"
		label := "Spur stummschalten"
		if solo {
			id = "audio.trackSolo"
			label = "Spur solo"
		}
		bindings = append(bindings, Binding{ID: id, Enabled: hasTrack, Run: func() {
			err := s.Edit(label, func(p *model.Project) error {
				t, _ := p.Track(s.SelectedTrack)
				if t == nil {
					return model.ErrNotFound
				}
				if t.Locked {
					return model.ErrLocked
				}
				if solo {
					t.Solo = !t.Solo
				} else {
					t.Muted = !t.Muted
				}
				return nil
			})
			if err != nil && report != nil {
				report(err)
			}
		}})
	}
	return bindings
}
