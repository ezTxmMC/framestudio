// Package model is the project data model of Frame Studio. All positions and lengths
// are in frames at the project frame rate. The model has no UI or I/O dependencies.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"image/color"
	"time"
)

// FormatVersion is the project file format. Version 2 stores the frame rate as a Rate
// (a number for integer rates, {"num","den"} otherwise) and adds settings.dropFrame; version 1
// files (integer "fps") still load unchanged.
const FormatVersion = 2

type ClipKind string

const (
	KindColor ClipKind = "color"
	KindTitle ClipKind = "title"
	KindImage ClipKind = "image"
	KindVideo ClipKind = "video"
	KindAudio ClipKind = "audio"
)

// IsVisual reports whether clips of this kind belong on video tracks.
func (k ClipKind) IsVisual() bool { return k != KindAudio }

type TrackKind string

const (
	TrackVideo TrackKind = "video"
	TrackAudio TrackKind = "audio"
)

// Accepts reports whether a clip of kind k may be placed on a track of this kind.
func (t TrackKind) Accepts(k ClipKind) bool {
	if t == TrackAudio {
		return k == KindAudio
	}
	return k.IsVisual()
}

// RGBA is a JSON-friendly non-premultiplied colour.
type RGBA struct{ R, G, B, A uint8 }

func (c RGBA) NRGBA() color.NRGBA        { return color.NRGBA{c.R, c.G, c.B, c.A} }
func FromNRGBA(c color.NRGBA) RGBA       { return RGBA{c.R, c.G, c.B, c.A} }
func (c RGBA) RGBA() (r, g, b, a uint32) { return c.NRGBA().RGBA() }

// Grade is a primary colour correction. All values are in -1..1, zero is neutral.
type Grade struct {
	Exposure    float64 `json:"exposure,omitempty"`
	Contrast    float64 `json:"contrast,omitempty"`
	Saturation  float64 `json:"saturation,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	Tint        float64 `json:"tint,omitempty"`
	Vignette    float64 `json:"vignette,omitempty"` // 0..1
}

func (g Grade) IsNeutral() bool { return g == Grade{} }

type Align string

const (
	AlignLeft   Align = "left"
	AlignCenter Align = "center"
	AlignRight  Align = "right"
)

// Title describes the text of a title clip.
type Title struct {
	Text       string  `json:"text"`
	Size       float64 `json:"size"` // fraction of frame height, e.g. 0.1
	Color      RGBA    `json:"color"`
	Bold       bool    `json:"bold,omitempty"`
	Align      Align   `json:"align,omitempty"`
	Y          float64 `json:"y"`          // vertical centre as fraction of frame height, 0.5 = middle
	Background RGBA    `json:"background"` // A=0 means none
}

type Fit string

const (
	FitContain Fit = "contain"
	FitCover   Fit = "cover"
	FitStretch Fit = "stretch"
)

type Clip struct {
	ID       string   `json:"id"`
	Kind     ClipKind `json:"kind"`
	Name     string   `json:"name"`
	Start    int      `json:"start"`  // timeline frame where the clip begins
	Length   int      `json:"length"` // frames, always >= 1
	MediaID  string   `json:"mediaId,omitempty"`
	SourceIn int      `json:"sourceIn,omitempty"` // first source frame used (video/audio)
	Color    RGBA     `json:"color"`              // label colour; fill colour of color clips
	Title    Title    `json:"title"`
	Grade    Grade    `json:"grade"`
	Opacity  float64  `json:"opacity"` // 0..1
	Volume   float64  `json:"volume"`  // linear gain, 1 = unity
	FadeIn   int      `json:"fadeIn,omitempty"`
	FadeOut  int      `json:"fadeOut,omitempty"`
	// FadeInFrom and FadeOutTo are the gain levels (0..1) where the fade ramps begin and end.
	// They are non-zero only on the parts of a clip that was split inside a fade, so the
	// curve stays continuous across the cut. FadeIn may then exceed Length (partial ramp).
	FadeInFrom float64 `json:"fadeInFrom,omitempty"`
	FadeOutTo  float64 `json:"fadeOutTo,omitempty"`
	Fit        Fit     `json:"fit,omitempty"`
}

func (c Clip) End() int { return c.Start + c.Length }

// Contains reports whether timeline frame f lies inside the clip.
func (c Clip) Contains(f int) bool { return f >= c.Start && f < c.End() }

// FadeFactor returns the 0..1 fade multiplier at timeline frame f.
func (c Clip) FadeFactor(f int) float64 {
	k := 1.0
	if c.FadeIn > 0 && f-c.Start < c.FadeIn {
		k = c.FadeInFrom + (1-c.FadeInFrom)*float64(f-c.Start)/float64(c.FadeIn)
	}
	if c.FadeOut > 0 && c.End()-f <= c.FadeOut {
		if o := c.FadeOutTo + (1-c.FadeOutTo)*float64(c.End()-f-1)/float64(c.FadeOut); o < k {
			k = o
		}
	}
	if k < 0 {
		return 0
	}
	return k
}

type Track struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Kind   TrackKind `json:"kind"`
	Muted  bool      `json:"muted,omitempty"`
	Hidden bool      `json:"hidden,omitempty"`
	Locked bool      `json:"locked,omitempty"`
	Solo   bool      `json:"solo,omitempty"`
	Volume float64   `json:"volume"`
	Clips  []Clip    `json:"clips"` // sorted by Start, never overlapping
}

// ClipAt returns the index of the clip covering frame f, or -1.
func (t *Track) ClipAt(f int) int {
	for i, c := range t.Clips {
		if c.Contains(f) {
			return i
		}
	}
	return -1
}

func (t *Track) End() int {
	if len(t.Clips) == 0 {
		return 0
	}
	return t.Clips[len(t.Clips)-1].End()
}

type MediaItem struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Path     string        `json:"path"`
	Kind     ClipKind      `json:"kind"` // image, video or audio
	Width    int           `json:"width,omitempty"`
	Height   int           `json:"height,omitempty"`
	Duration time.Duration `json:"duration,omitempty"` // zero for stills
	FPS      Rate          `json:"fps,omitzero"`       // source frame rate of videos
	HasAudio bool          `json:"hasAudio,omitempty"`
}

type Marker struct {
	ID    string `json:"id"`
	Frame int    `json:"frame"`
	Name  string `json:"name"`
	Color RGBA   `json:"color"`
}

type Settings struct {
	Width  int  `json:"width"`
	Height int  `json:"height"`
	FPS    Rate `json:"fps"` // project frame rate, see Rate for the JSON forms
	// DropFrame selects SMPTE drop-frame timecode display; it only has an effect at
	// 29.97 and 59.94 fps (Rate.CanDropFrame).
	DropFrame  bool `json:"dropFrame,omitempty"`
	Background RGBA `json:"background"`
}

type Project struct {
	Version  int         `json:"version"`
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Created  time.Time   `json:"created"`
	Modified time.Time   `json:"modified"`
	Settings Settings    `json:"settings"`
	Media    []MediaItem `json:"media"`
	Tracks   []Track     `json:"tracks"` // video tracks composite bottom (first) to top (last)
	Markers  []Marker    `json:"markers"`
}

func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewProject returns an empty 1080p25 project with two video tracks and one audio track.
func NewProject(name string) *Project {
	now := time.Now()
	p := &Project{
		Version: FormatVersion, ID: NewID(), Name: name, Created: now, Modified: now,
		Settings: Settings{Width: 1920, Height: 1080, FPS: Rate25, Background: RGBA{0, 0, 0, 255}},
		Media:    []MediaItem{}, Markers: []Marker{},
	}
	p.AddTrack(TrackVideo)
	p.AddTrack(TrackVideo)
	p.AddTrack(TrackAudio)
	return p
}

// Clone returns a deep copy, used for undo snapshots.
func (p *Project) Clone() *Project {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	var c Project
	if err := json.Unmarshal(b, &c); err != nil {
		panic(err)
	}
	return &c
}

// Duration is the end frame of the last clip on any track.
func (p *Project) Duration() int {
	end := 0
	for i := range p.Tracks {
		if e := p.Tracks[i].End(); e > end {
			end = e
		}
	}
	return end
}

// Rate is the project frame rate (25 fps if unset).
func (p *Project) Rate() Rate { return p.Settings.FPS.norm() }

// DropFrame reports whether timecodes are shown as drop-frame (only at 29.97 / 59.94).
func (p *Project) DropFrame() bool { return p.Settings.DropFrame && p.Rate().CanDropFrame() }

// FrameDuration is the length of one frame.
func (p *Project) FrameDuration() time.Duration { return p.Rate().FramesToDuration(1) }

// FramesToDuration is the time of frame f, exact (see Rate).
func (p *Project) FramesToDuration(f int) time.Duration { return p.Rate().FramesToDuration(f) }

// DurationToFrames is the number of whole project frames in d.
func (p *Project) DurationToFrames(d time.Duration) int { return p.Rate().DurationToFrames(d) }

// SecondFrames is the number of frames of a one-second step: the nominal rate (24 at
// 23.976, 30 at 29.97).
func (p *Project) SecondFrames() int { return p.Rate().Nominal() }

// Timecode formats frame f as HH:MM:SS:FF, or HH:MM:SS;FF in drop-frame mode.
func (p *Project) Timecode(f int) string { return Timecode(f, p.Rate(), p.DropFrame()) }

// ParseTimecode reads a full timecode in the project's counting mode.
func (p *Project) ParseTimecode(s string) (int, error) {
	return ParseTimecode(s, p.Rate(), p.DropFrame())
}

func (p *Project) Track(id string) (*Track, int) {
	for i := range p.Tracks {
		if p.Tracks[i].ID == id {
			return &p.Tracks[i], i
		}
	}
	return nil, -1
}

// Clip finds a clip by ID and returns it with its track index and clip index.
func (p *Project) Clip(id string) (*Clip, int, int) {
	for ti := range p.Tracks {
		for ci := range p.Tracks[ti].Clips {
			if p.Tracks[ti].Clips[ci].ID == id {
				return &p.Tracks[ti].Clips[ci], ti, ci
			}
		}
	}
	return nil, -1, -1
}

func (p *Project) MediaByID(id string) (*MediaItem, bool) {
	for i := range p.Media {
		if p.Media[i].ID == id {
			return &p.Media[i], true
		}
	}
	return nil, false
}

// MediaFrames is the length of a media item in project frames (0 for stills).
//
// Durations come from ffprobe as decimal seconds and are a hair below the exact frame
// boundary as often as above it (4.003999 s for 120 frames at 29.97), so a microsecond of
// tolerance (a hundredth of a frame at 240 fps) keeps whole-frame media whole.
func (p *Project) MediaFrames(m MediaItem) int {
	if m.Duration <= 0 {
		return 0
	}
	return p.DurationToFrames(m.Duration + time.Microsecond)
}

// VideoTracks / AudioTracks return the track indices of each kind in compositing order.
func (p *Project) TrackIndices(kind TrackKind) []int {
	var out []int
	for i, t := range p.Tracks {
		if t.Kind == kind {
			out = append(out, i)
		}
	}
	return out
}

// EditPoints returns the sorted, de-duplicated clip boundaries across all tracks, including 0.
func (p *Project) EditPoints() []int {
	seen := map[int]bool{0: true}
	pts := []int{0}
	for _, t := range p.Tracks {
		for _, c := range t.Clips {
			for _, f := range []int{c.Start, c.End()} {
				if !seen[f] {
					seen[f] = true
					pts = append(pts, f)
				}
			}
		}
	}
	sortInts(pts)
	return pts
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
