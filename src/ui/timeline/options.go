package timeline

import (
	"sort"

	"framestudio-proto/src/model"
)

// Playhead follow modes (settings Playback.Follow).
const (
	FollowPage   = "page"   // page the view when the playhead leaves it
	FollowSmooth = "smooth" // keep the playhead in the middle while playing
	FollowOff    = "off"    // never scroll on its own
)

// SetTrackHeights sets the lane heights of tracks the user has not resized; 0 keeps the
// built-in default.
func (t *Timeline) SetTrackHeights(video, audio float32) {
	d := rowDefaults{video, audio}
	if d == t.rows {
		return
	}
	t.rows = d
	t.Refresh()
}

// TrackHeights returns the effective default lane heights.
func (t *Timeline) TrackHeights() (video, audio float32) {
	return t.rows.of(model.TrackVideo), t.rows.of(model.TrackAudio)
}

// SetShowFilmstrip shows or hides the picture tiles of video and image clips.
func (t *Timeline) SetShowFilmstrip(on bool) {
	if t.noFilmstrip != !on {
		t.noFilmstrip = !on
		t.Refresh()
	}
}

func (t *Timeline) ShowFilmstrip() bool { return !t.noFilmstrip }

// SetShowWaveforms shows or hides the waveforms of audio clips.
func (t *Timeline) SetShowWaveforms(on bool) {
	if t.noWaveforms != !on {
		t.noWaveforms = !on
		t.Refresh()
	}
}

func (t *Timeline) ShowWaveforms() bool { return !t.noWaveforms }

// SetFollow sets how the view follows the playhead (FollowPage, FollowSmooth, FollowOff);
// unknown values mean FollowPage.
func (t *Timeline) SetFollow(mode string) {
	switch mode {
	case FollowSmooth, FollowOff:
	default:
		mode = FollowPage
	}
	t.followMode = mode
}

func (t *Timeline) Follow() string { return t.followMode }

// SetRippleSync chooses whether ripple edits (ripple trim, "Lücke schließen") shift every
// unlocked track (on, the default) or only the tracks of the edited clips.
func (t *Timeline) SetRippleSync(on bool) { t.noRippleSync = !on }

func (t *Timeline) RippleSync() bool { return !t.noRippleSync }

// SetStillSeconds sets the length of stills dropped on the timeline.
func (t *Timeline) SetStillSeconds(sec float64) {
	if sec > 0 {
		t.stillSecs = sec
	}
}

func (t *Timeline) stillLen(p *model.Project) int {
	return max(1, int(t.stillSecs*float64(max(p.Settings.FPS, 1))+0.5))
}

// StartRename opens the inline name entry in the header of track id.
func (t *Timeline) StartRename(trackID string) { t.startRename(trackID) }

// CanEscape reports whether Escape would change anything (see Escape).
func (t *Timeline) CanEscape() bool {
	return t.rename != nil || (t.drag != nil && t.drag.mode != dragNone && t.drag.mode != dragIgnore) ||
		len(t.s.Selected) > 0 || t.s.SelectedClip != ""
}

// RippleDelete deletes clips ids and closes the gaps they leave. With sync every
// unlocked track shifts (model.DeleteClips); without, only the tracks the deleted clips
// were on, so the other tracks keep their timing. A clip in the way gives a
// *model.RippleBlockedError; run it inside a session edit, which rolls back on error.
func RippleDelete(p *model.Project, ids []string, sync bool) error {
	if sync {
		return p.DeleteClips(ids, true)
	}
	type span struct{ from, to int }
	spans := map[string][]span{} // track ID -> deleted ranges
	for _, id := range ids {
		c, ti, _ := p.Clip(id)
		if c == nil {
			return model.ErrNotFound
		}
		tr := p.Tracks[ti].ID
		spans[tr] = append(spans[tr], span{c.Start, c.End()})
	}
	if err := p.DeleteClips(ids, false); err != nil {
		return err
	}
	for tr, list := range spans {
		sort.Slice(list, func(i, j int) bool { return list[i].from < list[j].from })
		var merged []span
		for _, s := range list {
			if n := len(merged); n > 0 && s.from <= merged[n-1].to {
				merged[n-1].to = max(merged[n-1].to, s.to)
				continue
			}
			merged = append(merged, s)
		}
		for i := len(merged) - 1; i >= 0; i-- {
			if err := shiftTracks(p, merged[i].to, merged[i].from-merged[i].to, map[string]bool{tr: true}); err != nil {
				return err
			}
		}
	}
	return nil
}

// shiftTracks is model.ShiftAfter limited to the tracks in only (by ID).
func shiftTracks(p *model.Project, at, shift int, only map[string]bool, except ...string) error {
	for _, tr := range p.Tracks {
		if only[tr.ID] {
			continue
		}
		for _, c := range tr.Clips {
			except = append(except, c.ID)
		}
	}
	return p.ShiftAfter(at, shift, except...)
}
