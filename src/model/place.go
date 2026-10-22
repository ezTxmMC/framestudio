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
// sound also gets its sound as an audio clip on the first unlocked audio track; if that is
// impossible nothing is placed. It returns the ID of the picture (or audio) clip.
func (p *Project) PlaceMedia(m MediaItem, at, stillLen int, preferred string) (string, error) {
	c := p.ClipFromMedia(m, max(at, 0), stillLen)
	t, err := p.TargetTrack(m.Kind, preferred)
	if err != nil {
		return "", err
	}
	tid := t.ID // AddTrack below may move tracks in memory
	var sound *Track
	if m.Kind == KindVideo && m.HasAudio {
		if sound, err = p.TargetTrack(KindAudio, ""); err != nil {
			return "", err
		}
	}
	var soundID string
	if sound != nil {
		soundID = sound.ID
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
