package model

// LockedTracksError reports that every track of a kind is locked, so a new clip has
// nowhere to go. It matches ErrLocked with errors.Is.
type LockedTracksError struct {
	Kind TrackKind
}

func (e *LockedTracksError) Error() string {
	if e.Kind == TrackAudio {
		return "Alle Audiospuren sind gesperrt. Eine Audiospur entsperren und erneut einfügen"
	}
	return "Alle Videospuren sind gesperrt. Eine Videospur entsperren und erneut einfügen"
}

func (e *LockedTracksError) Unwrap() error { return ErrLocked }

// TargetTrack picks the track for a new clip of kind k: preferred if it accepts k and is
// unlocked, otherwise the first unlocked track of the right kind. Without any track of
// that kind one is created; if all are locked a *LockedTracksError is returned.
func (p *Project) TargetTrack(k ClipKind, preferred string) (*Track, error) {
	if t, _ := p.Track(preferred); t != nil && t.Kind.Accepts(k) && !t.Locked {
		return t, nil
	}
	tk := TrackVideo
	if !k.IsVisual() {
		tk = TrackAudio
	}
	idx := p.TrackIndices(tk)
	for _, i := range idx {
		if !p.Tracks[i].Locked {
			return &p.Tracks[i], nil
		}
	}
	if len(idx) > 0 {
		return nil, &LockedTracksError{Kind: tk}
	}
	return p.AddTrack(tk), nil
}

// PlaceMedia overwrites media item m onto the timeline at frame at, on preferred or the
// next fitting unlocked track (see TargetTrack). Stills get stillLen frames. A video with
// sound also gets its sound as an audio clip on the track SoundTrack picks; if that is
// impossible nothing is placed. It returns the ID of the picture (or audio) clip.
func (p *Project) PlaceMedia(m MediaItem, at, stillLen int, preferred string) (string, error) {
	return p.PlaceMediaTo(m, at, stillLen, preferred, preferred) // an audio track takes the sound
}

// PlaceMediaTo is PlaceMedia with an explicit choice for the sound of a video:
// preferredAudio (for example the audio track under the pointer of a drop) wins when it is
// an unlocked audio track. Otherwise SoundTrack decides.
func (p *Project) PlaceMediaTo(m MediaItem, at, stillLen int, preferred, preferredAudio string) (string, error) {
	at = max(at, 0)
	c := p.ClipFromMedia(m, at, stillLen)
	if !m.Kind.IsVisual() {
		// pure audio: an audio track under the pointer wins, else the sound rule
		if tr, _ := p.Track(preferred); tr != nil && tr.Kind == TrackAudio {
			preferredAudio = preferred
		}
		pi := -1
		if tr, i := p.Track(preferred); tr != nil && tr.Kind == TrackVideo {
			pi = i
		}
		ti, err := p.SoundTrack(pi, preferredAudio, c.Start, c.End())
		if err != nil {
			return "", err
		}
		tid := p.ensureAudio(ti)
		if err := p.PlaceClip(tid, c); err != nil {
			return "", err
		}
		return c.ID, nil
	}
	t, err := p.TargetTrack(m.Kind, preferred)
	if err != nil {
		return "", err
	}
	tid := t.ID // AddTrack below may move tracks in memory
	soundAt := -3
	if m.Kind == KindVideo && m.HasAudio {
		_, pi := p.Track(tid)
		if soundAt, err = p.SoundTrack(pi, preferredAudio, c.Start, c.End()); err != nil {
			return "", err
		}
	}
	var soundID string
	if soundAt != -3 {
		soundID = p.ensureAudio(soundAt)
	}
	if err := p.PlaceClip(tid, c); err != nil {
		return "", err
	}
	if soundID != "" {
		a := c
		a.ID = NewID()
		a.Kind = KindAudio
		a.Color = RGBA{0x3D, 0x9A, 0x5B, 0xFF}
		if err := p.PlaceClip(soundID, a); err != nil {
			return "", err
		}
	}
	return c.ID, nil
}

// ensureAudio returns the ID of audio track index i, creating a new audio track for -1.
func (p *Project) ensureAudio(i int) string {
	if i < 0 {
		return p.AddTrack(TrackAudio).ID
	}
	return p.Tracks[i].ID
}

// SoundTrack picks the audio track for sound placed over [from, to) next to the picture
// on track index picture (-1 for none), without changing the project. It returns a track
// index, or -1 when a new audio track should be created.
//
// The rule: preferredAudio when it is an unlocked audio track (the user pointed at it);
// else the audio track at the same position among its kind as the picture track (V2 ->
// A2) when it is free over the range or holds only the sound of the pictures being
// overwritten; else the first unlocked audio track that is free over the range; else a new
// audio track. Existing sound is never overwritten unless the user pointed at its track.
// With every audio track locked it returns a *LockedTracksError.
func (p *Project) SoundTrack(picture int, preferredAudio string, from, to int) (int, error) {
	if tr, i := p.Track(preferredAudio); tr != nil && tr.Kind == TrackAudio && !tr.Locked {
		return i, nil
	}
	audio := p.TrackIndices(TrackAudio)
	unlocked := 0
	for _, i := range audio {
		if !p.Tracks[i].Locked {
			unlocked++
		}
	}
	if len(audio) > 0 && unlocked == 0 {
		return 0, &LockedTracksError{Kind: TrackAudio}
	}
	if picture >= 0 && picture < len(p.Tracks) && p.Tracks[picture].Kind == TrackVideo {
		pos := 0
		for i, x := range p.TrackIndices(TrackVideo) {
			if x == picture {
				pos = i
			}
		}
		if pos < len(audio) {
			ai := audio[pos]
			if !p.Tracks[ai].Locked && p.soundReplaceable(ai, picture, from, to) {
				return ai, nil
			}
		}
	}
	for _, i := range audio {
		if !p.Tracks[i].Locked && p.Tracks[i].free(from, to) {
			return i, nil
		}
	}
	return -1, nil
}

// free reports whether no clip of the track overlaps [from, to).
func (t *Track) free(from, to int) bool {
	for _, c := range t.Clips {
		if c.Start < to && c.End() > from {
			return false
		}
	}
	return true
}

// soundReplaceable reports whether audio track ai holds nothing over [from, to) except the
// linked sound of picture clips on track picture that the new picture overwrites.
func (p *Project) soundReplaceable(ai, picture, from, to int) bool {
	for _, c := range p.Tracks[ai].Clips {
		if c.Start >= to || c.End() <= from {
			continue
		}
		linked := false
		if picture >= 0 && c.MediaID != "" {
			for _, v := range p.Tracks[picture].Clips {
				if v.MediaID == c.MediaID && v.Start == c.Start && v.Length == c.Length && v.Start < to && v.End() > from {
					linked = true
					break
				}
			}
		}
		if !linked {
			return false
		}
	}
	return true
}

// LinkedClips returns the IDs of the clips on other tracks that form a pair with clip id:
// the same media, start, length and source offset (the picture and sound of one video).
func (p *Project) LinkedClips(id string) []string {
	c, ti, _ := p.Clip(id)
	if c == nil || c.MediaID == "" {
		return nil
	}
	var out []string
	for i, tr := range p.Tracks {
		if i == ti {
			continue
		}
		for _, o := range tr.Clips {
			if o.MediaID == c.MediaID && o.Start == c.Start && o.Length == c.Length && o.SourceIn == c.SourceIn {
				out = append(out, o.ID)
			}
		}
	}
	return out
}
