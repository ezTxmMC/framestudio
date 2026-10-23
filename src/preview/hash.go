package preview

import (
	"fmt"
	"hash/fnv"
	"os"

	"framestudio-proto/src/model"
)

// Hasher gives every timeline frame a hash of everything that influences its picture:
// the visible clips (content, position, source, grade, fades, title), their media
// files (path, size, mtime), track visibility and the canvas. Audio, selection,
// markers and names are left out, so those edits never invalidate cached pictures,
// and an edit changes the hash only of the frames its clips cover.
//
// Build is cheap (one hash per clip); FrameHash is O(tracks + clips on a track).
type Hasher struct {
	proj  *model.Project
	base  uint64
	clips [][]uint64 // [track][clip]
}

// BuildHasher hashes p. It stats the media files, so call it off the UI thread.
func BuildHasher(p *model.Project) *Hasher { return buildHasher(p, true) }

// PictureSig is one hash of everything FrameHash covers for all frames (without
// stat-ing the media files): equal signatures mean the edit left every picture alone.
func PictureSig(p *model.Project) uint64 {
	h := buildHasher(p, false)
	x := h.base
	if p == nil {
		return x
	}
	for ti, row := range h.clips {
		x = (x ^ uint64(ti+1)) * 1099511628211
		if p.Tracks[ti].Hidden {
			x ^= 0x517cc1b727220a95
		}
		if p.Tracks[ti].Kind == model.TrackVideo {
			x ^= 0x2545f4914f6cdd1d
		}
		for _, c := range row {
			x = (x ^ c) * 1099511628211
		}
	}
	return x
}

func buildHasher(p *model.Project, stat bool) *Hasher {
	h := &Hasher{proj: p}
	if p == nil {
		return h
	}
	hs := fnv.New64a()
	fmt.Fprintf(hs, "%dx%d@%v bg%v", p.Settings.Width, p.Settings.Height, p.Settings.FPS, p.Settings.Background)
	h.base = hs.Sum64()

	media := make(map[string]uint64, len(p.Media))
	for _, m := range p.Media {
		var size, mtime int64
		if !stat {
		} else if st, err := os.Stat(m.Path); err == nil {
			size, mtime = st.Size(), st.ModTime().UnixNano()
		}
		mh := fnv.New64a()
		fmt.Fprintf(mh, "%s|%v|%d|%d|%d|%d|%v", m.Path, m.Kind, m.Width, m.Height, size, mtime, m.FPS)
		media[m.ID] = mh.Sum64()
	}
	h.clips = make([][]uint64, len(p.Tracks))
	for ti := range p.Tracks {
		t := &p.Tracks[ti]
		if t.Kind != model.TrackVideo {
			continue
		}
		h.clips[ti] = make([]uint64, len(t.Clips))
		for ci, c := range t.Clips {
			c.ID, c.Name, c.Volume = "", "", 0
			if c.Kind != model.KindColor {
				c.Color = model.RGBA{}
			}
			ch := fnv.New64a()
			fmt.Fprintf(ch, "%+v|%d", c, media[c.MediaID])
			h.clips[ti][ci] = ch.Sum64()
		}
	}
	return h
}

// FrameHash returns the hash of timeline frame f. skipGrade distinguishes the
// before/after compare picture.
func (h *Hasher) FrameHash(f int, skipGrade bool) uint64 {
	x := h.base
	if skipGrade {
		x ^= 0x9e3779b97f4a7c15
	}
	if h.proj == nil {
		return x
	}
	for ti := range h.proj.Tracks {
		t := &h.proj.Tracks[ti]
		if t.Kind != model.TrackVideo || t.Hidden {
			continue
		}
		ci := t.ClipAt(f)
		if ci < 0 || !t.Clips[ci].Kind.IsVisual() {
			continue
		}
		x = (x ^ h.clips[ti][ci]) * 1099511628211
		x = (x ^ uint64(ti+1)) * 1099511628211
	}
	return x
}
