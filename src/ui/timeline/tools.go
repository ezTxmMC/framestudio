package timeline

import (
	"framestudio-proto/src/model"
)

// Ripple and roll edits. The model has no such operations, so they are built here on top
// of the project data; the widget runs them inside one session.Edit.

// unbounded stands in for "no limit" in trim ranges.
const unbounded = 1 << 30

// sourceFrames returns the length of a clip's timed source in project frames, or 0 for
// clips without a fixed length (stills, titles, colour mattes, missing media).
func sourceFrames(p *model.Project, c model.Clip) int {
	if c.MediaID == "" {
		return 0
	}
	if m, ok := p.MediaByID(c.MediaID); ok && m.Duration > 0 {
		return p.MediaFrames(*m)
	}
	return 0
}

// clampFades keeps fades within a new length, like TrimClip does (an unfinished ramp left
// over from a split is kept).
func clampFades(c *model.Clip, oldLen int) {
	if c.FadeIn <= oldLen {
		c.FadeIn = min(c.FadeIn, c.Length)
	}
	if c.FadeOut <= oldLen {
		c.FadeOut = min(c.FadeOut, c.Length)
	}
}

// rippleLimits returns the allowed range of delta for a ripple trim of clip id. For the
// right edge delta moves the end; for the left edge it is how many frames are cut from
// the head (negative = head extended). The clip keeps at least one frame and stays within
// its source.
func rippleLimits(p *model.Project, id string, left bool) (lo, hi int) {
	c, _, _ := p.Clip(id)
	if c == nil {
		return 0, 0
	}
	n := sourceFrames(p, *c)
	if left {
		lo, hi = -unbounded, c.Length-1
		if n > 0 {
			lo = -c.SourceIn
		}
		return lo, hi
	}
	lo, hi = 1-c.Length, unbounded
	if n > 0 {
		hi = n - c.SourceIn - c.Length
	}
	return lo, hi
}

// rippleTrim trims an edge of clip id by delta frames and shifts every later clip on
// every unlocked track by the same amount, so no gap opens, nothing is overwritten and the
// tracks stay in sync. The linked sound (or picture) of the clip on another unlocked track
// is trimmed the same way. Right edge: the end moves by delta. Left edge: delta frames are
// cut from the head while the clip keeps its start, and the clips after it move by -delta.
// A clip on another track that is in the way gives a *model.RippleBlockedError.
func rippleTrim(p *model.Project, id string, left bool, delta int) error {
	return rippleTrimSync(p, id, left, delta, true)
}

// rippleTrimSync is rippleTrim; without sync only the tracks of the trimmed clip and its
// linked partner shift (settings Timeline.RippleSyncLock off).
func rippleTrimSync(p *model.Project, id string, left bool, delta int, sync bool) error {
	c, ti, _ := p.Clip(id)
	if c == nil {
		return model.ErrNotFound
	}
	if p.Tracks[ti].Locked {
		return model.ErrLocked
	}
	if delta == 0 {
		return nil
	}
	lo, hi := rippleLimits(p, id, left)
	if delta < lo || delta > hi {
		return model.ErrInvalidEdit
	}
	oldEnd := c.End()
	ids := []string{id}
	for _, l := range p.LinkedClips(id) {
		if _, lt, _ := p.Clip(l); p.Tracks[lt].Locked {
			if c.LinkID != "" {
				return model.ErrLocked
			}
		} else {
			ids = append(ids, l)
		}
	}
	for _, x := range ids {
		l, h := rippleLimits(p, x, left)
		if delta < l || delta > h {
			return model.ErrInvalidEdit
		}
	}
	for _, x := range ids {
		c, _, _ := p.Clip(x)
		oldLen := c.Length
		timed := sourceFrames(p, *c) > 0
		if left {
			c.Length -= delta
			if timed {
				c.SourceIn += delta
			}
		} else {
			c.Length += delta
		}
		clampFades(c, oldLen)
	}
	shift := delta
	if left {
		shift = -delta
	}
	if !sync {
		only := map[string]bool{}
		for _, x := range ids {
			_, xt, _ := p.Clip(x)
			only[p.Tracks[xt].ID] = true
		}
		return shiftTracks(p, oldEnd, shift, only, ids...)
	}
	return p.ShiftAfter(oldEnd, shift, ids...)
}

// rollNeighbours returns the clip indices on either side of the cut at an edge of clip ci,
// or ok false when the clip does not touch a neighbour there.
func rollNeighbours(tr *model.Track, ci int, leftEdge bool) (a, b int, ok bool) {
	if leftEdge {
		if ci > 0 && tr.Clips[ci-1].End() == tr.Clips[ci].Start {
			return ci - 1, ci, true
		}
		return 0, 0, false
	}
	if ci+1 < len(tr.Clips) && tr.Clips[ci].End() == tr.Clips[ci+1].Start {
		return ci, ci + 1, true
	}
	return 0, 0, false
}

// rollLimits returns the range the cut between the adjacent clips a and b may move to:
// both keep at least one frame, a cannot run past the end of its source and b cannot
// start before the start of its source.
func rollLimits(p *model.Project, aID, bID string) (lo, hi int) {
	a, _, _ := p.Clip(aID)
	b, _, _ := p.Clip(bID)
	if a == nil || b == nil {
		return 0, 0
	}
	lo, hi = a.Start+1, b.End()-1
	if n := sourceFrames(p, *a); n > 0 {
		hi = min(hi, a.Start+n-a.SourceIn)
	}
	if sourceFrames(p, *b) > 0 {
		lo = max(lo, b.Start-b.SourceIn)
	}
	return lo, hi
}

// rollEdit moves the cut between the adjacent clips a and b to frame cut: a's end and b's
// start move together, so the programme length stays the same.
func rollEdit(p *model.Project, aID, bID string, cut int) error {
	work := p.Clone()
	a, _, _ := p.Clip(aID)
	b, _, _ := p.Clip(bID)
	if a == nil || b == nil {
		return model.ErrNotFound
	}
	delta := cut - b.Start
	left, right := p.ExplicitGroup(aID), p.ExplicitGroup(bID)
	if len(left) == 1 && len(right) == 1 {
		return rollSingle(p, aID, bID, cut)
	}
	used := map[string]bool{}
	for _, id := range left {
		x, ti, _ := work.Clip(id)
		partner := ""
		for _, r := range right {
			if y, tj, _ := work.Clip(r); ti == tj && x.End() == y.Start {
				partner = r
				break
			}
		}
		if partner == "" {
			return model.ErrInvalidEdit
		}
		y, _, _ := work.Clip(partner)
		if err := rollSingle(work, id, partner, y.Start+delta); err != nil {
			return err
		}
		used[partner] = true
	}
	if len(used) != len(right) {
		return model.ErrInvalidEdit
	}
	*p = *work
	return nil
}

func rollSingle(p *model.Project, aID, bID string, cut int) error {
	a, ta, _ := p.Clip(aID)
	b, tb, _ := p.Clip(bID)
	if a == nil || b == nil {
		return model.ErrNotFound
	}
	if ta != tb || a.End() != b.Start {
		return model.ErrInvalidEdit
	}
	if p.Tracks[ta].Locked {
		return model.ErrLocked
	}
	if cut == b.Start {
		return nil
	}
	lo, hi := rollLimits(p, aID, bID)
	if cut < lo || cut > hi {
		return model.ErrInvalidEdit
	}
	aLen, bLen := a.Length, b.Length
	d := cut - b.Start
	a.Length += d
	b.Start, b.Length = cut, b.Length-d
	if sourceFrames(p, *b) > 0 {
		b.SourceIn += d
	}
	clampFades(a, aLen)
	clampFades(b, bLen)
	return nil
}

// insertTrackNear adds a track of the same kind as track id directly above or below it in
// the display (video tracks are shown top layer first, so "above" means a higher layer).
func insertTrackNear(p *model.Project, id string, above bool) (*model.Track, error) {
	ref, _ := p.Track(id)
	if ref == nil {
		return nil, model.ErrNotFound
	}
	kind, refID := ref.Kind, ref.ID
	added := *p.AddTrack(kind)
	// take it out again and re-insert it next to the reference
	_, ai := p.Track(added.ID)
	p.Tracks = append(p.Tracks[:ai], p.Tracks[ai+1:]...)
	_, ri := p.Track(refID)
	at := ri // before the reference in Project.Tracks
	if (kind == model.TrackVideo) == above {
		at = ri + 1
	}
	p.Tracks = append(p.Tracks, model.Track{})
	copy(p.Tracks[at+1:], p.Tracks[at:])
	p.Tracks[at] = added
	return &p.Tracks[at], nil
}
