package export

import (
	"fmt"
	"math"
	"strings"

	"framestudio-proto/src/model"
)

// AudioMix is an ffmpeg audio mixdown: one input per audible clip and a filter_complex
// whose output pad is [aout].
type AudioMix struct {
	Inputs []string // media paths, passed as consecutive -i arguments
	Filter string
}

// audioPart is the audible portion of one clip inside the export range.
type audioPart struct {
	clip  model.Clip
	media model.MediaItem
	gain  float64
}

// audioParts collects the clips that sound in [from,to): audio tracks only, muted tracks
// skipped, and if any audio track is solo only solo tracks. available filters media (nil = all).
func audioParts(p *model.Project, from, to int, available func(model.MediaItem) bool) []audioPart {
	solo := false
	for _, t := range p.Tracks {
		if t.Kind == model.TrackAudio && t.Solo {
			solo = true
		}
	}
	var parts []audioPart
	for _, t := range p.Tracks {
		if t.Kind != model.TrackAudio || t.Muted || (solo && !t.Solo) {
			continue
		}
		for _, c := range t.Clips {
			if c.Kind != model.KindAudio || c.End() <= from || c.Start >= to {
				continue
			}
			gain := t.Volume * c.Volume
			if gain <= 0 {
				continue
			}
			m, ok := p.MediaByID(c.MediaID)
			if !ok || m.Path == "" {
				continue
			}
			if m.Kind != model.KindAudio && !(m.Kind == model.KindVideo && m.HasAudio) {
				continue
			}
			if available != nil && !available(*m) {
				continue
			}
			parts = append(parts, audioPart{clip: c, media: *m, gain: gain})
		}
	}
	return parts
}

// HasAudio reports whether any clip is audible in [from,to).
func HasAudio(p *model.Project, from, to int) bool {
	return len(audioParts(p, from, to, nil)) > 0
}

// BuildAudioMix builds the mixdown of [from,to) as a pure function of the project. Input
// numbering starts at firstInput (e.g. 1 when ffmpeg input 0 is the video pipe). available
// may exclude media that cannot be read (nil = all). ok is false if nothing is audible.
func BuildAudioMix(p *model.Project, from, to, firstInput int, available func(model.MediaItem) bool) (mix AudioMix, ok bool) {
	parts := audioParts(p, from, to, available)
	if len(parts) == 0 || p.Settings.FPS <= 0 {
		return AudioMix{}, false
	}
	fps := float64(p.Settings.FPS)
	// Every position is a whole sample at 48 kHz, rounded like the live engine's
	// FramesToSamples, so cuts at 24 or 30 fps (frames that are not whole milliseconds)
	// join without gaps or overlaps and export matches what the engine plays.
	smp := func(frames int) int64 { return int64(math.Round(float64(frames) * 48000 / fps)) }
	var b strings.Builder
	for i, part := range parts {
		c := part.clip
		mix.Inputs = append(mix.Inputs, part.media.Path)
		n := smp(c.End()) - smp(c.Start) // clip length in samples
		src := smp(c.SourceIn)
		// aresample pads a stream that starts late (and fills timestamp gaps) so sample
		// n is time n/48000 of the file, as in the live engine's decoder; the sample-exact
		// atrim below then cuts by time.
		fmt.Fprintf(&b, "[%d:a:0]aresample=async=1:first_pts=0,aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo,", firstInput+i)
		fmt.Fprintf(&b, "atrim=start_sample=%d:end_sample=%d,asetpts=PTS-STARTPTS", src, src+n)
		// Pad short sources so fades and offsets refer to the full clip length.
		fmt.Fprintf(&b, ",apad=whole_len=%d", n)
		if c.FadeIn > 0 {
			// FadeIn may exceed the clip (unfinished ramp after a split); afade simply stops.
			fmt.Fprintf(&b, ",afade=t=in:ss=0:ns=%d", smp(c.FadeIn))
			if c.FadeInFrom > 0 {
				fmt.Fprintf(&b, ":silence=%s", ftoa(c.FadeInFrom))
			}
		}
		if c.FadeOut > 0 {
			fo, unity := c.FadeOut, 1.0
			if fo > c.Length { // ramp began before the clip: start at the gain reached there
				unity = c.FadeOutTo + (1-c.FadeOutTo)*float64(c.Length)/float64(fo)
				fo = c.Length
			}
			fmt.Fprintf(&b, ",afade=t=out:ss=%d:ns=%d", n-smp(fo), smp(fo))
			if c.FadeOutTo > 0 {
				fmt.Fprintf(&b, ":silence=%s", ftoa(c.FadeOutTo))
			}
			if unity < 1 {
				fmt.Fprintf(&b, ":unity=%s", ftoa(unity))
			}
		}
		s, e := max(c.Start, from), min(c.End(), to)
		if s > c.Start || e < c.End() {
			fmt.Fprintf(&b, ",atrim=start_sample=%d:end_sample=%d,asetpts=PTS-STARTPTS", smp(s)-smp(c.Start), smp(e)-smp(c.Start))
		}
		if part.gain != 1 {
			fmt.Fprintf(&b, ",volume=%s", ftoa(part.gain))
		}
		if d := smp(s) - smp(from); d > 0 {
			// adelay emits its padding without timestamps; rebuild them from the sample
			// count or amix/apad would drop the leading silence.
			fmt.Fprintf(&b, ",adelay=delays=%dS:all=1,asetpts=N/SR/TB", d)
		}
		fmt.Fprintf(&b, "[a%d];", i)
	}
	for i := range parts {
		fmt.Fprintf(&b, "[a%d]", i)
	}
	total := smp(to) - smp(from)
	if len(parts) > 1 {
		fmt.Fprintf(&b, "amix=inputs=%d:duration=longest:dropout_transition=0:normalize=0,", len(parts))
	}
	fmt.Fprintf(&b, "apad=whole_len=%d,atrim=end_sample=%d,asetpts=PTS-STARTPTS[aout]", total, total)
	mix.Filter = b.String()
	return mix, true
}

// ftoa formats seconds or gains compactly for filter arguments.
func ftoa(v float64) string {
	s := fmt.Sprintf("%.6f", v)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
