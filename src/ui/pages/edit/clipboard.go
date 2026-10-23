package edit

import (
	"fmt"
	"math"
	"sort"

	"framestudio-proto/src/model"
)

// The clip clipboard is shared by every open project (UI thread only): it keeps copies
// of the clips and of the media items they use, so clips paste into another project too.
var board clipboard

type clipboard struct {
	entries []clipEntry
	media   []model.MediaItem
	fps     int
}

// clipEntry is one copied clip with its place relative to the copied group: lane is the
// track among the tracks of its kind counted from the lowest one used, offset the frames
// after the earliest start.
type clipEntry struct {
	clip   model.Clip
	kind   model.TrackKind
	lane   int
	offset int
}

func clipboardFull() bool { return len(board.entries) > 0 }

// copyClips captures clips ids of p.
func copyClips(p *model.Project, ids []string) clipboard {
	type pos struct {
		c    model.Clip
		kind model.TrackKind
		idx  int // position among the tracks of its kind
	}
	var list []pos
	low := map[model.TrackKind]int{}
	start := math.MaxInt
	media := map[string]bool{}
	var cb clipboard
	for _, id := range ids {
		c, ti, _ := p.Clip(id)
		if c == nil {
			continue
		}
		kind := p.Tracks[ti].Kind
		idx := 0
		for k, i := range p.TrackIndices(kind) {
			if i == ti {
				idx = k
			}
		}
		if l, ok := low[kind]; !ok || idx < l {
			low[kind] = idx
		}
		start = min(start, c.Start)
		list = append(list, pos{*c, kind, idx})
		if c.MediaID != "" && !media[c.MediaID] {
			if m, ok := p.MediaByID(c.MediaID); ok {
				media[c.MediaID] = true
				cb.media = append(cb.media, *m)
			}
		}
	}
	for _, x := range list {
		cb.entries = append(cb.entries, clipEntry{clip: x.c, kind: x.kind, lane: x.idx - low[x.kind], offset: x.c.Start - start})
	}
	sort.SliceStable(cb.entries, func(i, j int) bool { return cb.entries[i].offset < cb.entries[j].offset })
	cb.fps = p.Settings.FPS
	return cb
}

func (p *Page) copySelection() {
	ids := p.selection()
	if len(ids) == 0 {
		return
	}
	board = copyClips(p.ctx.Session.Project, ids)
	p.reg.Refresh() // "Einfügen" becomes available
}

// cut copies the selection and removes it (the gap stays).
func (p *Page) cut() {
	ids := p.selection()
	if len(ids) == 0 {
		return
	}
	cb := copyClips(p.ctx.Session.Project, ids)
	label := "Clip ausschneiden"
	if len(ids) > 1 {
		label = fmt.Sprintf("%d Clips ausschneiden", len(ids))
	}
	if p.edit(label, func(pr *model.Project) error { return pr.DeleteClips(ids, false) }) == nil {
		board = cb
		p.reg.Refresh()
	}
}

// paste places the clipboard at the playhead: the lowest copied track of each kind goes
// to the selected track when it is of that kind (else the first one), the others keep
// their distance above it; missing tracks are added. Media the clips use joins the
// project (a file already in it is reused). The pasted clips are selected and the
// playhead moves to their end.
func (p *Page) paste() {
	if !clipboardFull() {
		return
	}
	s := p.ctx.Session
	cb := board
	at := s.Playhead
	sel := s.SelectedTrack
	var ids []string
	end := at
	label := "Clip einfügen"
	if len(cb.entries) > 1 {
		label = fmt.Sprintf("%d Clips einfügen", len(cb.entries))
	}
	err := p.edit(label, func(pr *model.Project) error {
		ids, end = ids[:0], at
		mediaID := adoptMedia(pr, cb.media)
		conv := func(f int) int { return f }
		if cb.fps > 0 && cb.fps != pr.Settings.FPS {
			conv = func(f int) int { return scaleFrames(f, cb.fps, pr.Settings.FPS) }
		}
		base := map[model.TrackKind]int{}
		if t, ti := pr.Track(sel); t != nil {
			for k, i := range pr.TrackIndices(t.Kind) {
				if i == ti {
					base[t.Kind] = k
				}
			}
		}
		for _, e := range cb.entries {
			lane := base[e.kind] + e.lane
			for len(pr.TrackIndices(e.kind)) <= lane {
				pr.AddTrack(e.kind)
			}
			tr := pr.Tracks[pr.TrackIndices(e.kind)[lane]]
			if tr.Locked {
				return model.ErrLocked
			}
			c := e.clip
			c.ID = model.NewID()
			c.Start = at + conv(e.offset)
			c.Length = max(1, conv(c.Length))
			c.SourceIn = conv(c.SourceIn)
			c.FadeIn, c.FadeOut = conv(c.FadeIn), conv(c.FadeOut)
			if c.MediaID != "" {
				c.MediaID = mediaID[c.MediaID]
			}
			if err := pr.PlaceClip(tr.ID, c); err != nil {
				return err
			}
			ids = append(ids, c.ID)
			end = max(end, c.End())
		}
		return nil
	})
	if err == nil {
		s.SelectClips(ids)
		s.Seek(end)
	}
}

// adoptMedia makes the copied media items part of pr and returns the IDs to use: the
// same item, an item of pr with the same file, or the added copy.
func adoptMedia(pr *model.Project, items []model.MediaItem) map[string]string {
	ids := map[string]string{}
	for _, m := range items {
		if own, ok := pr.MediaByID(m.ID); ok && own.Path == m.Path {
			ids[m.ID] = m.ID
			continue
		}
		found := ""
		if m.Path != "" {
			for _, own := range pr.Media {
				if own.Path == m.Path {
					found = own.ID
					break
				}
			}
		}
		if found == "" {
			add := m
			if _, taken := pr.MediaByID(m.ID); taken {
				add.ID = model.NewID()
			}
			found = pr.AddMedia(add)
		}
		ids[m.ID] = found
	}
	return ids
}

// duplicate places copies of the selection right after it on the same tracks
// (overwriting what is there) and selects them; the clipboard stays as it is.
func (p *Page) duplicate() {
	s := p.ctx.Session
	ids := p.selection()
	if len(ids) == 0 {
		return
	}
	from, to := math.MaxInt, 0
	for _, id := range ids {
		if c, _, _ := s.Project.Clip(id); c != nil {
			from, to = min(from, c.Start), max(to, c.End())
		}
	}
	shift := to - from
	var out []string
	end := to
	label := "Clip duplizieren"
	if len(ids) > 1 {
		label = fmt.Sprintf("%d Clips duplizieren", len(ids))
	}
	if p.edit(label, func(pr *model.Project) error {
		out, end = out[:0], to
		type place struct {
			track string
			c     model.Clip
		}
		var list []place
		for _, id := range ids {
			c, ti, _ := pr.Clip(id)
			if c == nil {
				return model.ErrNotFound
			}
			d := *c
			d.ID = model.NewID()
			d.Start += shift
			list = append(list, place{pr.Tracks[ti].ID, d})
		}
		for _, x := range list {
			if err := pr.PlaceClip(x.track, x.c); err != nil {
				return err
			}
			out = append(out, x.c.ID)
			end = max(end, x.c.End())
		}
		return nil
	}) == nil {
		s.SelectClips(out)
		s.Seek(end)
	}
}
