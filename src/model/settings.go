package model

// SetSettings applies new project settings (resolution, frame rate, drop-frame, background).
// When the frame rate changes, every frame position (clip start, length, source in, fades,
// markers) is converted so everything keeps its time in seconds; clips that would overlap
// afterwards are pushed behind their predecessor, and clips never reach past their source.
// A resolution change touches no clip. Callers wrap it in Session.Edit /
// Session.SetSettings for undo. On error the project may be half changed: Session.Edit
// restores it.
func (p *Project) SetSettings(st Settings) error {
	st.FPS = st.FPS.Reduced()
	if !st.FPS.CanDropFrame() {
		st.DropFrame = false // drop-frame only exists at 29.97 and 59.94
	}
	if err := ValidateSettings(st); err != nil {
		return err
	}
	old := p.Rate()
	p.Settings = st
	if old.Equal(st.FPS) {
		return p.Validate()
	}
	conv := func(f int) int { return ConvertFrames(f, old, st.FPS) }
	for ti := range p.Tracks {
		prevEnd := 0
		for ci := range p.Tracks[ti].Clips {
			c := &p.Tracks[ti].Clips[ci]
			start := max(conv(c.Start), prevEnd)
			end := max(conv(c.End()), start+1)
			c.Start, c.Length = start, end-start
			c.SourceIn = conv(c.SourceIn)
			if m, ok := p.MediaByID(c.MediaID); ok && m.Duration > 0 {
				if limit := p.MediaFrames(*m) - c.SourceIn; c.Length > limit && limit >= 1 {
					c.Length = limit
				}
			}
			// A fade of a split clip (FadeInFrom/FadeOutTo > 0) may be longer than the clip;
			// clamping it would change the curve, so only full fades are limited.
			c.FadeIn = conv(c.FadeIn)
			if c.FadeInFrom == 0 {
				c.FadeIn = min(c.FadeIn, c.Length)
			}
			c.FadeOut = conv(c.FadeOut)
			if c.FadeOutTo == 0 {
				c.FadeOut = min(c.FadeOut, c.Length)
			}
			prevEnd = c.End()
		}
	}
	for i := range p.Markers {
		p.Markers[i].Frame = conv(p.Markers[i].Frame)
	}
	return p.Validate()
}
