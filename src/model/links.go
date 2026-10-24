package model

// AudioCount keeps projects imported before stream counts were stored usable.
func (m MediaItem) AudioCount() int {
	if m.AudioStreams > 0 {
		return m.AudioStreams
	}
	if m.HasAudio || m.Kind == KindAudio {
		return 1
	}
	return 0
}

// ExplicitGroup includes id and its stored editing partners, without inferring links.
func (p *Project) ExplicitGroup(id string) []string {
	out := []string{id}
	if c, _, _ := p.Clip(id); c != nil && c.LinkID != "" {
		out = append(out, p.LinkedClips(id)...)
	}
	return out
}

// ExpandLinks deduplicates a selection and keeps the requested primary clip last.
func (p *Project) ExpandLinks(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		for _, partner := range p.ExplicitGroup(id) {
			if !seen[partner] {
				seen[partner] = true
				out = append(out, partner)
			}
		}
	}
	if len(ids) > 0 {
		primary := ids[len(ids)-1]
		for i, id := range out {
			if id == primary {
				out = append(append(out[:i], out[i+1:]...), primary)
				break
			}
		}
	}
	return out
}

// LinkClips merges existing groups and the chosen clips. All checks precede changes.
func (p *Project) LinkClips(ids []string) error {
	ids = p.ExpandLinks(ids)
	if len(ids) < 2 {
		return ErrInvalidEdit
	}
	for _, id := range ids {
		c, ti, _ := p.Clip(id)
		if c == nil {
			return ErrNotFound
		}
		if p.Tracks[ti].Locked {
			return ErrLocked
		}
	}
	group := NewID()
	for _, id := range ids {
		c, _, _ := p.Clip(id)
		c.LinkID, c.Unlinked = group, false
	}
	return nil
}

// UnlinkClips dissolves the selected groups, including partners on other tracks.
func (p *Project) UnlinkClips(ids []string) error {
	all := append([]string(nil), ids...)
	for _, id := range ids {
		all = append(all, p.LinkedClips(id)...)
	}
	for _, id := range all {
		c, ti, _ := p.Clip(id)
		if c == nil {
			return ErrNotFound
		}
		if p.Tracks[ti].Locked {
			return ErrLocked
		}
	}
	for _, id := range all {
		c, _, _ := p.Clip(id)
		c.LinkID, c.Unlinked = "", true
	}
	return nil
}

// TrimClip applies the same edge deltas to every linked clip, preserving offsets.
func (p *Project) TrimClip(id string, start, length int) error {
	c, _, _ := p.Clip(id)
	if c == nil {
		return ErrNotFound
	}
	left, right := start-c.Start, start+length-c.End()
	work := p.Clone()
	for _, partner := range p.ExplicitGroup(id) {
		x, _, _ := work.Clip(partner)
		if x == nil {
			return ErrInvalidEdit
		}
		if err := work.trimClip(partner, x.Start+left, x.Length+right-left); err != nil {
			return err
		}
	}
	*p = *work
	return nil
}

// SplitClip gives the right-hand pieces their own group so later edits stay local
// to that side of the cut. A locked partner prevents the entire cut.
func (p *Project) SplitClip(id string, frame int) (string, error) {
	c, _, _ := p.Clip(id)
	if c == nil {
		return "", ErrNotFound
	}
	if frame <= c.Start || frame >= c.End() {
		return "", ErrInvalidEdit
	}
	work := p.Clone()
	group, rightID := NewID(), ""
	for _, partner := range p.ExplicitGroup(id) {
		x, ti, _ := work.Clip(partner)
		if work.Tracks[ti].Locked {
			return "", ErrLocked
		}
		if frame <= x.Start || frame >= x.End() {
			continue
		}
		r, err := work.splitClip(partner, frame)
		if err != nil {
			return "", err
		}
		if c.LinkID != "" {
			next, _, _ := work.Clip(r)
			next.LinkID = group
		}
		if partner == id {
			rightID = r
		}
	}
	*p = *work
	return rightID, nil
}
