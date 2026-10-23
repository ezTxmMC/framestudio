package audio

import (
	"math"

	"framestudio-proto/src/model"
)

// Plan is a project's audio prepared for mixing: every audio track with its clips in
// sample units and their decoded sources. Plans are immutable once built, so the mixer
// goroutine reads them without locks.
type Plan struct {
	FPS    int
	Tracks []TrackPlan // all audio tracks, in project order
	Err    error       // a problem that makes the mix silent (e.g. ErrNoFFmpeg)
	keys   map[string]string
}

// TrackPlan is one audio track. Audible is false for muted tracks and, when any
// track is solo, for the tracks that are not.
type TrackPlan struct {
	ID      string
	Audible bool
	Clips   []ClipPlan // sorted by Start
}

// ClipPlan is one audio clip in timeline samples. Gains and fades follow export's
// ffmpeg mixdown (afade, volume) sample for sample.
type ClipPlan struct {
	Start, End int64   // timeline sample range [Start, End)
	Src        int64   // source sample frame played at Start
	Gain       float64 // track volume x clip volume
	FadeIn     float64 // ramp length in samples (may exceed the clip), 0 = none
	FadeInFrom float64 // gain at the first sample of the ramp
	FadeOut    float64 // ramp length in samples within the clip, 0 = none
	FadeOutTo  float64 // gain the ramp ends at
	Unity      float64 // gain where the fade-out ramp starts (< 1 after a split inside it)
	Source     Source  // nil: offline or unreadable, plays silence
}

// FramesToSamples converts a timeline frame position to a sample position.
func FramesToSamples(f, fps int) int64 {
	if fps <= 0 {
		return 0
	}
	return int64(math.Round(float64(f) * SampleRate / float64(fps)))
}

// BuildPlan prepares p for mixing. source returns the decoded audio of a media item
// (nil, err when unavailable); it is called for every audio clip, also on muted tracks,
// so their audio is ready when they are switched back on. Clips follow export's rules:
// audio tracks only, clips of kind audio whose media is audio or video with sound.
func BuildPlan(p *model.Project, source func(m model.MediaItem) (Source, error)) *Plan {
	fps := p.Settings.FPS
	pl := &Plan{FPS: fps, keys: map[string]string{}}
	solo := false
	for _, t := range p.Tracks {
		if t.Kind == model.TrackAudio && t.Solo {
			solo = true
		}
	}
	sources := map[string]Source{}
	for _, t := range p.Tracks {
		if t.Kind != model.TrackAudio {
			continue
		}
		tp := TrackPlan{ID: t.ID, Audible: !t.Muted && (!solo || t.Solo)}
		for _, c := range t.Clips {
			if c.Kind != model.KindAudio || c.Length <= 0 {
				continue
			}
			m, ok := p.MediaByID(c.MediaID)
			if !ok || m.Path == "" || (m.Kind != model.KindAudio && !(m.Kind == model.KindVideo && m.HasAudio)) {
				continue
			}
			src, seen := sources[m.ID]
			if !seen && source != nil {
				var err error
				src, err = source(*m)
				if err == ErrNoFFmpeg && pl.Err == nil {
					pl.Err = err
				}
				if pcm, ok := src.(*PCM); ok && pcm != nil {
					pl.keys[m.ID] = pcm.key
				}
				sources[m.ID] = src
			}
			tp.Clips = append(tp.Clips, clipPlan(c, t.Volume*c.Volume, fps, src))
		}
		pl.Tracks = append(pl.Tracks, tp)
	}
	return pl
}

func clipPlan(c model.Clip, gain float64, fps int, src Source) ClipPlan {
	cp := ClipPlan{
		Start:  FramesToSamples(c.Start, fps),
		End:    FramesToSamples(c.End(), fps),
		Src:    FramesToSamples(c.SourceIn, fps),
		Gain:   gain,
		Unity:  1,
		Source: src,
	}
	if c.FadeIn > 0 {
		cp.FadeIn = float64(FramesToSamples(c.FadeIn, fps))
		cp.FadeInFrom = c.FadeInFrom
	}
	if c.FadeOut > 0 {
		fo := c.FadeOut
		if fo > c.Length { // the ramp began before the clip: start at the gain reached there
			cp.Unity = c.FadeOutTo + (1-c.FadeOutTo)*float64(c.Length)/float64(fo)
			fo = c.Length
		}
		cp.FadeOut = float64(FramesToSamples(fo, fps))
		cp.FadeOutTo = c.FadeOutTo
	}
	return cp
}

// gainAt is the clip gain at clip-relative sample i (afade's linear "tri" curve).
func (c *ClipPlan) gainAt(i int64) float64 {
	g := c.Gain
	if c.FadeIn > 0 && float64(i) < c.FadeIn {
		g *= c.FadeInFrom + (1-c.FadeInFrom)*float64(i)/c.FadeIn
	}
	if c.FadeOut > 0 {
		st := float64(c.End-c.Start) - c.FadeOut
		if k := float64(i) - st; k >= 0 {
			g *= c.FadeOutTo + (c.Unity-c.FadeOutTo)*(c.FadeOut-k)/c.FadeOut
		}
	}
	return g
}

// Audible reports whether any audible clip overlaps the timeline samples [from, to).
func (pl *Plan) Audible(from, to int64) bool {
	if pl == nil {
		return false
	}
	for _, t := range pl.Tracks {
		if !t.Audible {
			continue
		}
		for _, c := range t.Clips {
			if c.Gain > 0 && c.Source != nil && c.Start < to && c.End > from {
				return true
			}
		}
	}
	return false
}

// Window limits rendering to the timeline samples [Lo, Hi); Hi <= Lo means no limit.
type Window struct{ Lo, Hi int64 }

func (w Window) contains(p int64) bool { return w.Hi <= w.Lo || (p >= w.Lo && p < w.Hi) }

// Render mixes n sample frames starting at timeline sample pos, advancing step samples
// per output frame (1 normal, 2 double speed, -1 reverse). tracks[i] receives track i's
// post-fader signal (interleaved stereo, len >= 2n, overwritten; silent tracks are
// zeroed); out receives the sum (overwritten). Nothing is clipped here.
func (pl *Plan) Render(pos int64, step int, win Window, out []float32, tracks [][]float32, n int) {
	clear(out[:2*n])
	if step == 0 {
		step = 1
	}
	span := int64(abs(step))
	lo, hi := pos, pos+int64(step)*int64(n) // samples touched, in some order
	if lo > hi {
		lo, hi = hi, lo
	}
	hi += span
	for ti := range pl.Tracks {
		t := &pl.Tracks[ti]
		buf := tracks[ti][:2*n]
		clear(buf)
		if !t.Audible {
			continue
		}
		for ci := range t.Clips {
			c := &t.Clips[ci]
			if c.Source == nil || c.Gain <= 0 || c.End <= lo || c.Start >= hi {
				continue
			}
			for i := range n {
				p0 := pos + int64(step)*int64(i)
				var l, r float32
				for k := range span { // box filter when skipping samples
					p := p0 + k
					if step < 0 {
						p = p0 - k
					}
					if p < c.Start || p >= c.End || !win.contains(p) {
						continue
					}
					sl, sr, ok := c.Source.Frame(c.Src + p - c.Start)
					if !ok {
						continue
					}
					g := float32(c.gainAt(p - c.Start))
					l += sl * g
					r += sr * g
				}
				if span > 1 {
					l /= float32(span)
					r /= float32(span)
				}
				buf[2*i] += l
				buf[2*i+1] += r
			}
		}
		for i, v := range buf {
			out[i] += v
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// knee is the level above which the master limiter starts to bend the signal.
const knee = 0.9

// SoftClip limits a master sample to (-1, 1): unchanged below the knee (so normal
// levels match export exactly), smoothly saturating above it.
func SoftClip(x float32) float32 {
	a := x
	if a < 0 {
		a = -a
	}
	if a <= knee {
		return x
	}
	y := float32(knee + (1-knee)*math.Tanh(float64(a-knee)/(1-knee)))
	if x < 0 {
		return -y
	}
	return y
}
