package model

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrNotFound    = errors.New("nicht gefunden")
	ErrLocked      = errors.New("Spur ist gesperrt")
	ErrWrongTrack  = errors.New("Clip passt nicht auf diese Spurart")
	ErrInvalidEdit = errors.New("ungültige Bearbeitung")
)

// NewClip returns a clip with sensible defaults for its kind.
func NewClip(kind ClipKind, name string, start, length int) Clip {
	c := Clip{
		ID: NewID(), Kind: kind, Name: name, Start: start, Length: length,
		Opacity: 1, Volume: 1, Fit: FitContain,
		Color: RGBA{0x3F, 0x73, 0xB5, 0xFF},
	}
	if kind == KindTitle {
		c.Title = Title{Text: name, Size: 0.1, Color: RGBA{255, 255, 255, 255}, Bold: true, Align: AlignCenter, Y: 0.5}
		c.Color = RGBA{0x74, 0x66, 0xC2, 0xFF}
	}
	return c
}

// AddTrack appends a new track of the given kind with an automatic name (V1, V2, A1...).
func (p *Project) AddTrack(kind TrackKind) *Track {
	prefix := "V"
	if kind == TrackAudio {
		prefix = "A"
	}
	used := map[string]bool{}
	for _, t := range p.Tracks {
		used[t.Name] = true
	}
	n := 1 // lowest free number, so names stay unique after a track was removed
	for used[fmt.Sprintf("%s%d", prefix, n)] {
		n++
	}
	t := Track{ID: NewID(), Name: fmt.Sprintf("%s%d", prefix, n), Kind: kind, Volume: 1, Clips: []Clip{}}
	// keep video tracks before audio tracks
	if kind == TrackVideo {
		idx := len(p.TrackIndices(TrackVideo))
		p.Tracks = append(p.Tracks, Track{})
		copy(p.Tracks[idx+1:], p.Tracks[idx:])
		p.Tracks[idx] = t
		return &p.Tracks[idx]
	}
	p.Tracks = append(p.Tracks, t)
	return &p.Tracks[len(p.Tracks)-1]
}

// RemoveTrack deletes a track and its clips. Locked tracks are refused.
func (p *Project) RemoveTrack(id string) error {
	t, i := p.Track(id)
	if i < 0 {
		return ErrNotFound
	}
	if t.Locked {
		return ErrLocked
	}
	p.Tracks = append(p.Tracks[:i], p.Tracks[i+1:]...)
	return nil
}

// clearRange removes [from,to) from a track, trimming or splitting clips that overlap it.
func (t *Track) clearRange(from, to int) {
	var out []Clip
	for _, c := range t.Clips {
		switch {
		case c.End() <= from || c.Start >= to:
			out = append(out, c)
		case c.Start >= from && c.End() <= to:
			// fully covered, drop
		case c.Start < from && c.End() > to:
			left, _ := splitAt(c, from)
			_, right := splitAt(c, to)
			right.ID = NewID()
			out = append(out, left, right)
		case c.Start < from:
			left, _ := splitAt(c, from)
			out = append(out, left)
		default: // overlaps the right edge of the range
			_, right := splitAt(c, to)
			right.ID = c.ID
			out = append(out, right)
		}
	}
	if out == nil {
		out = []Clip{}
	}
	t.Clips = out
}

func (t *Track) sortClips() {
	sort.SliceStable(t.Clips, func(i, j int) bool { return t.Clips[i].Start < t.Clips[j].Start })
}

// PlaceClip overwrites the clip onto the track at c.Start, like an NLE overwrite edit.
func (p *Project) PlaceClip(trackID string, c Clip) error {
	t, _ := p.Track(trackID)
	if t == nil {
		return ErrNotFound
	}
	if t.Locked {
		return ErrLocked
	}
	if !t.Kind.Accepts(c.Kind) {
		return ErrWrongTrack
	}
	if c.Length < 1 || c.Start < 0 {
		return ErrInvalidEdit
	}
	if c.ID == "" {
		c.ID = NewID()
	}
	t.clearRange(c.Start, c.End())
	t.Clips = append(t.Clips, c)
	t.sortClips()
	return nil
}

// MoveClip moves a clip to another start frame and optionally another track (overwrite).
func (p *Project) MoveClip(clipID, toTrackID string, start int) error {
	c, ti, ci := p.Clip(clipID)
	if c == nil {
		return ErrNotFound
	}
	if p.Tracks[ti].Locked {
		return ErrLocked
	}
	dst, _ := p.Track(toTrackID)
	if dst == nil {
		return ErrNotFound
	}
	if dst.Locked {
		return ErrLocked
	}
	if !dst.Kind.Accepts(c.Kind) {
		return ErrWrongTrack
	}
	if start < 0 {
		start = 0
	}
	moved := *c
	moved.Start = start
	src := &p.Tracks[ti]
	src.Clips = append(src.Clips[:ci], src.Clips[ci+1:]...)
	return p.PlaceClip(toTrackID, moved)
}

// TrimClip sets new start and length. Moving the start shifts SourceIn so the
// content stays anchored to the timeline. Media-backed clips cannot extend past their source.
func (p *Project) TrimClip(clipID string, start, length int) error {
	c, ti, ci := p.Clip(clipID)
	if c == nil {
		return ErrNotFound
	}
	t := &p.Tracks[ti]
	if t.Locked {
		return ErrLocked
	}
	if length < 1 || start < 0 {
		return ErrInvalidEdit
	}
	in := c.SourceIn + (start - c.Start)
	timed := false // only video/audio sources have a fixed length to stay within
	if c.MediaID != "" {
		if m, ok := p.MediaByID(c.MediaID); ok && m.Duration > 0 {
			timed = true
			if in < 0 || in+length > p.MediaFrames(*m) {
				return ErrInvalidEdit
			}
		}
	}
	if !timed {
		in = 0
	}
	trimmed := *c
	trimmed.Start, trimmed.Length, trimmed.SourceIn = start, length, in
	// Fades follow the clip edges and never exceed the new length (an unfinished ramp
	// left over from a split is kept as it is).
	if c.FadeIn <= c.Length {
		trimmed.FadeIn = min(trimmed.FadeIn, length)
	}
	if c.FadeOut <= c.Length {
		trimmed.FadeOut = min(trimmed.FadeOut, length)
	}
	t.Clips = append(t.Clips[:ci], t.Clips[ci+1:]...)
	t.clearRange(start, start+length)
	t.Clips = append(t.Clips, trimmed)
	t.sortClips()
	return nil
}

// splitAt cuts c at timeline frame f (c.Start < f < c.End()). The fade curve stays
// continuous: a part that starts or ends inside a fade gets a ramp that begins or ends at
// the gain the original clip had there. The right part gets a new ID.
func splitAt(c Clip, f int) (left, right Clip) {
	left, right = c, c
	left.Length = f - c.Start
	right.ID = NewID()
	right.Start = f
	right.Length = c.End() - f
	right.SourceIn += f - c.Start

	// Fade-in: the left part keeps the ramp (possibly unfinished); the right part
	// continues it from the gain reached at f.
	if d := f - c.Start; c.FadeIn > d {
		right.FadeIn = c.FadeIn - d
		right.FadeInFrom = c.FadeInFrom + (1-c.FadeInFrom)*float64(d)/float64(c.FadeIn)
	} else {
		right.FadeIn, right.FadeInFrom = 0, 0
	}
	// Fade-out: the right part keeps the ramp; the left part ends at the gain it had at f-1.
	if d := c.End() - f; c.FadeOut > d {
		left.FadeOut = c.FadeOut - d
		left.FadeOutTo = c.FadeOutTo + (1-c.FadeOutTo)*float64(d)/float64(c.FadeOut)
	} else {
		left.FadeOut, left.FadeOutTo = 0, 0
	}
	return left, right
}

// SplitClip cuts the clip at timeline frame f and returns the ID of the right part.
func (p *Project) SplitClip(clipID string, f int) (string, error) {
	c, ti, ci := p.Clip(clipID)
	if c == nil {
		return "", ErrNotFound
	}
	t := &p.Tracks[ti]
	if t.Locked {
		return "", ErrLocked
	}
	if f <= c.Start || f >= c.End() {
		return "", ErrInvalidEdit
	}
	left, right := splitAt(*c, f)
	t.Clips[ci] = left
	t.Clips = append(t.Clips, right)
	t.sortClips()
	return right.ID, nil
}

// SplitAll cuts every unlocked track at frame f (blade across all tracks).
func (p *Project) SplitAll(f int) {
	for ti := range p.Tracks {
		t := &p.Tracks[ti]
		if t.Locked {
			continue
		}
		if ci := t.ClipAt(f); ci >= 0 {
			_, _ = p.SplitClip(t.Clips[ci].ID, f)
		}
	}
}

// DeleteClip removes a clip. With ripple, later clips on the same track close the gap.
func (p *Project) DeleteClip(clipID string, ripple bool) error {
	c, ti, ci := p.Clip(clipID)
	if c == nil {
		return ErrNotFound
	}
	t := &p.Tracks[ti]
	if t.Locked {
		return ErrLocked
	}
	removed := *c
	t.Clips = append(t.Clips[:ci], t.Clips[ci+1:]...)
	if ripple {
		for i := range t.Clips {
			if t.Clips[i].Start >= removed.End() {
				t.Clips[i].Start -= removed.Length
			}
		}
	}
	return nil
}

// UpdateClip replaces a clip's editable properties. Start, Length and Kind are kept;
// use MoveClip/TrimClip to change timing.
func (p *Project) UpdateClip(c Clip) error {
	cur, ti, _ := p.Clip(c.ID)
	if cur == nil {
		return ErrNotFound
	}
	if p.Tracks[ti].Locked {
		return ErrLocked
	}
	c.Start, c.Length, c.Kind, c.SourceIn, c.MediaID = cur.Start, cur.Length, cur.Kind, cur.SourceIn, cur.MediaID
	*cur = c
	return nil
}

// AddMedia registers a media item, returning its ID.
func (p *Project) AddMedia(m MediaItem) string {
	if m.ID == "" {
		m.ID = NewID()
	}
	p.Media = append(p.Media, m)
	return m.ID
}

// RemoveMedia deletes a media item and every clip that uses it. It returns ErrLocked
// without changes if any of those clips sits on a locked track.
func (p *Project) RemoveMedia(id string) error {
	idx := -1
	for i := range p.Media {
		if p.Media[i].ID == id {
			idx = i
		}
	}
	if idx < 0 {
		return ErrNotFound
	}
	for _, t := range p.Tracks {
		if !t.Locked {
			continue
		}
		for _, c := range t.Clips {
			if c.MediaID == id {
				return ErrLocked
			}
		}
	}
	p.Media = append(p.Media[:idx], p.Media[idx+1:]...)
	for ti := range p.Tracks {
		var keep []Clip
		for _, c := range p.Tracks[ti].Clips {
			if c.MediaID != id {
				keep = append(keep, c)
			}
		}
		if keep == nil {
			keep = []Clip{}
		}
		p.Tracks[ti].Clips = keep
	}
	return nil
}

// ClipFromMedia builds a clip for a media item starting at frame start.
// Stills get defaultLen frames; video and audio use their full length.
func (p *Project) ClipFromMedia(m MediaItem, start, defaultLen int) Clip {
	length := p.MediaFrames(m)
	if length < 1 {
		length = defaultLen
	}
	c := NewClip(m.Kind, m.Name, start, length)
	c.MediaID = m.ID
	switch m.Kind {
	case KindVideo:
		c.Color = RGBA{0x3F, 0x73, 0xB5, 0xFF}
	case KindImage:
		c.Color = RGBA{0x2F, 0x8F, 0x8B, 0xFF}
	case KindAudio:
		c.Color = RGBA{0x3D, 0x9A, 0x5B, 0xFF}
	}
	return c
}

func (p *Project) AddMarker(f int, name string) Marker {
	m := Marker{ID: NewID(), Frame: f, Name: name, Color: RGBA{0x5A, 0x8C, 0xEB, 0xFF}}
	p.Markers = append(p.Markers, m)
	sort.SliceStable(p.Markers, func(i, j int) bool { return p.Markers[i].Frame < p.Markers[j].Frame })
	return m
}

func (p *Project) RemoveMarker(id string) {
	for i, m := range p.Markers {
		if m.ID == id {
			p.Markers = append(p.Markers[:i], p.Markers[i+1:]...)
			return
		}
	}
}

// Validate checks structural invariants; Load uses it to reject corrupt files.
func (p *Project) Validate() error {
	if p.Settings.FPS <= 0 || p.Settings.Width <= 0 || p.Settings.Height <= 0 {
		return fmt.Errorf("ungültige Projekteinstellungen %+v", p.Settings)
	}
	ids := map[string]bool{}
	for _, t := range p.Tracks {
		prevEnd := 0
		for _, c := range t.Clips {
			if ids[c.ID] {
				return fmt.Errorf("doppelte Clip-ID %s", c.ID)
			}
			ids[c.ID] = true
			if c.Length < 1 || c.Start < prevEnd {
				return fmt.Errorf("Clip %q auf %s überlappt oder ist leer", c.Name, t.Name)
			}
			if !t.Kind.Accepts(c.Kind) {
				return fmt.Errorf("Clip %q passt nicht auf %s", c.Name, t.Name)
			}
			prevEnd = c.End()
		}
	}
	return nil
}
