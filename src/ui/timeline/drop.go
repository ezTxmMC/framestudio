package timeline

import (
	"fyne.io/fyne/v2"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
)

var _ kit.DropZone = (*Timeline)(nil)

// stillSeconds is the length of a still dropped on the timeline (same as the media page).
const stillSeconds = 5

// dropGhost is one clip a media drop would create.
type dropGhost struct {
	clip     model.Clip
	track    int             // index into Project.Tracks, -1 for a track the drop creates
	newKind  model.TrackKind // kind of the created track (track == -1)
	pictures bool            // picture clip (not linked audio)
}

// dropPlan is where dropped media would land: start frame, preferred track and whether a
// new track of newKind is created first (drop below the last track).
type dropPlan struct {
	ids       []string
	at        int
	preferred string // track ID under the pointer, "" for none
	newTrack  bool
	newKind   model.TrackKind
	snapAt    int // snapped frame, -1 for none
	ghosts    []dropGhost
}

// planDrop works out where media ids dropped at pos would go, with the same rules as
// model.PlaceMedia, without changing the project.
func (t *Timeline) planDrop(ids []string, pos fyne.Position) *dropPlan {
	p := t.s.Project
	g := t.geom()
	stillLen := stillSeconds * max(p.Settings.FPS, 1)
	var items []model.MediaItem
	total := 0
	for _, id := range ids {
		if m, ok := p.MediaByID(id); ok {
			items = append(items, *m)
			total += p.ClipFromMedia(*m, 0, stillLen).Length
		}
	}
	if len(items) == 0 {
		return nil
	}
	plan := &dropPlan{ids: ids, snapAt: -1}
	x := max(pos.X, headerW)
	plan.at = g.frameAt(x)
	if t.snapping {
		ts := snapTargets(p, t.s.Playhead, t.s.In, t.s.Out)
		if s, at, ok := snapSpan(plan.at, total, ts, float64(snapPx)/g.ppf); ok && s >= 0 {
			plan.at, plan.snapAt = s, at
		}
	}

	if ri := g.rowAt(pos.Y); ri >= 0 {
		plan.preferred = p.Tracks[g.rows[ri].track].ID
	} else if pos.Y >= g.tailY() && pos.Y >= rulerH {
		plan.newTrack = true
		plan.newKind = model.TrackVideo
		if !items[0].Kind.IsVisual() {
			plan.newKind = model.TrackAudio
		}
	}

	at := plan.at
	for _, m := range items {
		c := p.ClipFromMedia(m, at, stillLen)
		ti, kind := t.previewTarget(plan, m.Kind)
		plan.ghosts = append(plan.ghosts, dropGhost{clip: c, track: ti, newKind: kind, pictures: true})
		if m.Kind == model.KindVideo && m.HasAudio {
			a := c
			a.Kind = model.KindAudio
			a.Color = model.RGBA{R: 0x3D, G: 0x9A, B: 0x5B, A: 0xFF}
			ati, akind := previewTrack(p, model.KindAudio, "")
			plan.ghosts = append(plan.ghosts, dropGhost{clip: a, track: ati, newKind: akind})
		}
		at = c.End()
	}
	return plan
}

// previewTarget mirrors the track choice of the drop for a clip of kind k.
func (t *Timeline) previewTarget(plan *dropPlan, k model.ClipKind) (int, model.TrackKind) {
	if plan.newTrack && plan.newKind.Accepts(k) {
		return -1, plan.newKind
	}
	return previewTrack(t.s.Project, k, plan.preferred)
}

// previewTrack mirrors model.TargetTrack without changing the project: it returns the
// track index for a clip of kind k, or -1 when a new track of the returned kind would be
// created. With all tracks of the kind locked it returns -2.
func previewTrack(p *model.Project, k model.ClipKind, preferred string) (int, model.TrackKind) {
	if tr, i := p.Track(preferred); tr != nil && tr.Kind.Accepts(k) && !tr.Locked {
		return i, tr.Kind
	}
	tk := model.TrackVideo
	if !k.IsVisual() {
		tk = model.TrackAudio
	}
	idx := p.TrackIndices(tk)
	for _, i := range idx {
		if !p.Tracks[i].Locked {
			return i, tk
		}
	}
	if len(idx) > 0 {
		return -2, tk
	}
	return -1, tk
}

// MediaDragOver shows where dragged media would land.
func (t *Timeline) MediaDragOver(ids []string, pos fyne.Position) {
	t.drop = t.planDrop(ids, pos)
	t.Refresh()
}

// MediaDragLeave removes the drop preview.
func (t *Timeline) MediaDragLeave() {
	if t.drop != nil {
		t.drop = nil
		t.Refresh()
	}
}

// MediaDrop overwrites the media at the pointer as one undo step ("Medien einfügen"),
// back to back when several items are dropped, and selects the new clips.
func (t *Timeline) MediaDrop(ids []string, pos fyne.Position) bool {
	plan := t.planDrop(ids, pos)
	t.drop = nil
	if plan == nil {
		t.Refresh()
		return false
	}
	var placed []string
	err := t.s.Edit("Medien einfügen", func(p *model.Project) error {
		placed = placed[:0]
		stillLen := stillSeconds * max(p.Settings.FPS, 1)
		pref := plan.preferred
		if plan.newTrack {
			pref = p.AddTrack(plan.newKind).ID
		}
		at := plan.at
		for _, id := range plan.ids {
			m, ok := p.MediaByID(id)
			if !ok {
				continue
			}
			item := *m
			main, err := p.PlaceMedia(item, at, stillLen, pref)
			if err != nil {
				return err
			}
			placed = append(placed, main)
			c, _, _ := p.Clip(main)
			if item.Kind == model.KindVideo && item.HasAudio {
				if a := linkedAudio(p, item.ID, main, at); a != "" {
					placed = append(placed, a)
				}
			}
			at = c.End()
		}
		return nil
	})
	if err != nil {
		t.fail(editError("Medien einfügen", err))
		t.Refresh()
		return false
	}
	if len(placed) > 0 {
		// primary: the first picture clip
		sel := append(append([]string{}, placed[1:]...), placed[0])
		t.s.SelectClips(sel)
	}
	t.Refresh()
	return len(placed) > 0
}

// linkedAudio finds the sound clip PlaceMedia placed with picture clip main.
func linkedAudio(p *model.Project, mediaID, main string, at int) string {
	for _, tr := range p.Tracks {
		if tr.Kind != model.TrackAudio {
			continue
		}
		for _, c := range tr.Clips {
			if c.MediaID == mediaID && c.Start == at && c.ID != main && c.Kind == model.KindAudio {
				return c.ID
			}
		}
	}
	return ""
}
