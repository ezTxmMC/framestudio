// Package session holds the editing state of one open project: undo history, playhead,
// selection and playback. It is not goroutine-safe; the UI uses it from its own thread only.
package session

import (
	"math"
	"time"

	"framestudio-proto/src/model"
)

// Change flags tell subscribers what kind of state changed.
type Change uint

const (
	ChangeProject   Change = 1 << iota // tracks, clips, media, markers or settings
	ChangePlayhead                     // playhead moved
	ChangeSelection                    // selected clip/track/media changed
	ChangePlayback                     // play/pause/loop toggled
	ChangeMeta                         // name, path or dirty flag
)

const maxUndo = 200

type Session struct {
	Project *model.Project
	Path    string // file on disk, empty until first save
	Dirty   bool

	Playhead int
	Playing  bool
	Loop     bool

	// In and Out mark an optional range (export, loop playback). Out <= In means unset.
	In, Out int

	SelectedClip  string
	SelectedTrack string
	SelectedMedia string

	undo, redo []snapshot
	listeners  map[int]func(Change)
	nextID     int
	carry      time.Duration // sub-frame remainder while playing
}

type snapshot struct {
	label   string
	key     string
	at      time.Time
	project *model.Project
}

func New(p *model.Project) *Session {
	s := &Session{Project: p, listeners: map[int]func(Change){}}
	if len(p.Tracks) > 0 {
		s.SelectedTrack = p.Tracks[0].ID
	}
	return s
}

// Subscribe registers fn for change notifications and returns an unsubscribe func.
func (s *Session) Subscribe(fn func(Change)) func() {
	id := s.nextID
	s.nextID++
	s.listeners[id] = fn
	return func() { delete(s.listeners, id) }
}

func (s *Session) Notify(c Change) {
	for _, fn := range s.listeners {
		fn(c)
	}
}

func (s *Session) FPS() int { return s.Project.Settings.FPS }

// Edit applies fn to the project as one undoable step. If fn returns an error the
// project is restored and nothing is recorded.
func (s *Session) Edit(label string, fn func(p *model.Project) error) error {
	before := s.Project.Clone()
	if err := fn(s.Project); err != nil {
		s.Project = before
		return err
	}
	s.rescaleTime(before.Settings.FPS)
	s.undo = append(s.undo, snapshot{label: label, at: time.Now(), project: before})
	if len(s.undo) > maxUndo {
		s.undo = s.undo[1:]
	}
	s.redo = nil
	s.Project.Modified = time.Now()
	s.Dirty = true
	s.fixSelection()
	s.clampPlayhead()
	s.Notify(ChangeProject | ChangeMeta)
	return nil
}

// EditCoalesced is Edit for continuous controls such as sliders: consecutive edits with the
// same key within 1.5 s share one undo step, so dragging a fader is undone in one go.
func (s *Session) EditCoalesced(key, label string, fn func(p *model.Project) error) error {
	n := len(s.undo)
	if n == 0 || s.undo[n-1].key != key || time.Since(s.undo[n-1].at) > 1500*time.Millisecond {
		if err := s.Edit(label, fn); err != nil {
			return err
		}
		s.undo[len(s.undo)-1].key = key
		s.undo[len(s.undo)-1].at = time.Now()
		return nil
	}
	before := s.Project.Clone()
	if err := fn(s.Project); err != nil {
		s.Project = before
		return err
	}
	s.rescaleTime(before.Settings.FPS)
	s.undo[n-1].at = time.Now()
	s.redo = nil
	s.Project.Modified = time.Now()
	s.Dirty = true
	s.fixSelection()
	s.Notify(ChangeProject | ChangeMeta)
	return nil
}

func (s *Session) CanUndo() bool { return len(s.undo) > 0 }
func (s *Session) CanRedo() bool { return len(s.redo) > 0 }

// UndoLabel / RedoLabel describe the next step for menu items, e.g. "Clip teilen".
func (s *Session) UndoLabel() string {
	if !s.CanUndo() {
		return ""
	}
	return s.undo[len(s.undo)-1].label
}

func (s *Session) RedoLabel() string {
	if !s.CanRedo() {
		return ""
	}
	return s.redo[len(s.redo)-1].label
}

func (s *Session) Undo() {
	if !s.CanUndo() {
		return
	}
	last := s.undo[len(s.undo)-1]
	s.undo = s.undo[:len(s.undo)-1]
	s.redo = append(s.redo, snapshot{label: last.label, project: s.Project})
	old := s.Project.Settings.FPS
	s.Project = last.project
	s.rescaleTime(old)
	s.Dirty = true
	s.fixSelection()
	s.clampPlayhead()
	s.Notify(ChangeProject | ChangeMeta | ChangeSelection)
}

func (s *Session) Redo() {
	if !s.CanRedo() {
		return
	}
	next := s.redo[len(s.redo)-1]
	s.redo = s.redo[:len(s.redo)-1]
	s.undo = append(s.undo, snapshot{label: next.label, project: s.Project})
	old := s.Project.Settings.FPS
	s.Project = next.project
	s.rescaleTime(old)
	s.Dirty = true
	s.fixSelection()
	s.clampPlayhead()
	s.Notify(ChangeProject | ChangeMeta | ChangeSelection)
}

// MarkSaved records a successful save to path.
func (s *Session) MarkSaved(path string) {
	s.Path = path
	s.Dirty = false
	s.Notify(ChangeMeta)
}

// Rename changes the project name as an undoable edit.
func (s *Session) Rename(name string) {
	_ = s.Edit("Projekt umbenennen", func(p *model.Project) error {
		p.Name = name
		return nil
	})
}

// rescaleTime keeps playhead and In/Out at the same time in seconds when the project
// frame rate changed from old (by an edit, undo or redo).
func (s *Session) rescaleTime(old int) {
	fps := s.Project.Settings.FPS
	if old <= 0 || fps <= 0 || old == fps {
		return
	}
	conv := func(f int) int { return int(math.Round(float64(f) * float64(fps) / float64(old))) }
	s.Playhead, s.In, s.Out = conv(s.Playhead), conv(s.In), conv(s.Out)
	s.Notify(ChangePlayhead)
}

func (s *Session) fixSelection() {
	if s.SelectedClip != "" {
		if c, _, _ := s.Project.Clip(s.SelectedClip); c == nil {
			s.SelectedClip = ""
		}
	}
	if s.SelectedTrack != "" {
		if t, _ := s.Project.Track(s.SelectedTrack); t == nil {
			s.SelectedTrack = ""
		}
	}
	if s.SelectedTrack == "" && len(s.Project.Tracks) > 0 {
		s.SelectedTrack = s.Project.Tracks[0].ID
	}
	if s.SelectedMedia != "" {
		if _, ok := s.Project.MediaByID(s.SelectedMedia); !ok {
			s.SelectedMedia = ""
		}
	}
}

func (s *Session) clampPlayhead() {
	if s.Playhead < 0 {
		s.Playhead = 0
	}
}

// Seek moves the playhead to frame f (clamped at 0) and stops playback.
func (s *Session) Seek(f int) {
	s.SetPlaying(false)
	s.seek(f)
}

func (s *Session) seek(f int) {
	if f < 0 {
		f = 0
	}
	if f == s.Playhead {
		return
	}
	s.Playhead = f
	s.Notify(ChangePlayhead)
}

func (s *Session) SelectClip(id string) {
	if s.SelectedClip == id {
		return
	}
	s.SelectedClip = id
	if c, ti, _ := s.Project.Clip(id); c != nil {
		s.SelectedTrack = s.Project.Tracks[ti].ID
	}
	s.Notify(ChangeSelection)
}

func (s *Session) SelectTrack(id string) {
	s.SelectedTrack = id
	s.Notify(ChangeSelection)
}

func (s *Session) SelectMedia(id string) {
	s.SelectedMedia = id
	s.Notify(ChangeSelection)
}

// Clip returns the selected clip, if any.
func (s *Session) Clip() (*model.Clip, bool) {
	c, _, _ := s.Project.Clip(s.SelectedClip)
	return c, c != nil
}

func (s *Session) HasRange() bool { return s.Out > s.In }

func (s *Session) SetIn(f int)  { s.In = f; s.Notify(ChangePlayhead) }
func (s *Session) SetOut(f int) { s.Out = f; s.Notify(ChangePlayhead) }
func (s *Session) ClearRange()  { s.In, s.Out = 0, 0; s.Notify(ChangePlayhead) }

func (s *Session) SetLoop(on bool) {
	s.Loop = on
	s.Notify(ChangePlayback)
}

// SetPlaying starts or stops playback. Starting at the end rewinds to the start (or In).
func (s *Session) SetPlaying(on bool) {
	if on == s.Playing {
		return
	}
	if on {
		end := s.playEnd()
		if end <= 0 {
			return
		}
		if s.Playhead >= end {
			s.seek(s.playStart())
		}
		s.carry = 0
	}
	s.Playing = on
	s.Notify(ChangePlayback)
}

func (s *Session) TogglePlaying() { s.SetPlaying(!s.Playing) }

func (s *Session) playStart() int {
	if s.HasRange() {
		return s.In
	}
	return 0
}

func (s *Session) playEnd() int {
	if s.HasRange() {
		return s.Out
	}
	return s.Project.Duration()
}

// Advance moves the playhead by real time dt while playing. Called by the playback clock.
func (s *Session) Advance(dt time.Duration) {
	if !s.Playing {
		return
	}
	frame := s.Project.FrameDuration()
	s.carry += dt
	n := int(s.carry / frame)
	if n == 0 {
		return
	}
	s.carry -= time.Duration(n) * frame
	f := s.Playhead + n
	end := s.playEnd()
	if f >= end {
		if s.Loop && end > s.playStart() {
			f = s.playStart() + (f-end)%(end-s.playStart())
		} else {
			s.seek(end)
			s.Playing = false
			s.Notify(ChangePlayback)
			return
		}
	}
	s.seek(f)
}

// NextEdit / PrevEdit return the nearest edit point after/before the playhead.
func (s *Session) NextEdit() int {
	for _, f := range s.Project.EditPoints() {
		if f > s.Playhead {
			return f
		}
	}
	return s.Playhead
}

func (s *Session) PrevEdit() int {
	pts := s.Project.EditPoints()
	for i := len(pts) - 1; i >= 0; i-- {
		if pts[i] < s.Playhead {
			return pts[i]
		}
	}
	return 0
}
