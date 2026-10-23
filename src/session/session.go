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
	// Rate is the playback speed while playing: 1 normal, 2/4/8 fast forward,
	// negative values play backwards (J/K/L shuttle).
	Rate float64

	// In and Out mark an optional range (export, loop playback). Out <= In means unset.
	In, Out int

	// SelectedClip is the primary selected clip (inspector, colour page). Selected holds
	// every selected clip ID including the primary one, in selection order.
	SelectedClip  string
	Selected      []string
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
	s := &Session{Project: p, Rate: 1, listeners: map[int]func(Change){}}
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
	c := ChangeProject | ChangeMeta | s.fixSelection(before)
	s.clampPlayhead()
	s.Notify(c)
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
	s.Notify(ChangeProject | ChangeMeta | s.fixSelection(before))
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
	prev := s.Project
	s.Project = last.project
	s.rescaleTime(old)
	s.Dirty = true
	s.fixSelection(prev)
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
	prev := s.Project
	s.Project = next.project
	s.rescaleTime(old)
	s.Dirty = true
	s.fixSelection(prev)
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

// fixSelection drops selected clips that were deleted and, from a selection of several
// clips, those whose track is now locked (a locked member would make every group edit
// fail; a single clip stays selected so the inspector can explain the lock). It lets the selected
// track follow the primary clip when an edit moved it to another track. before is the
// project as it was before the change. It returns ChangeSelection when the selection
// changed, else 0.
func (s *Session) fixSelection(before *model.Project) Change {
	n0, clip0, track0, media0 := len(s.Selected), s.SelectedClip, s.SelectedTrack, s.SelectedMedia
	s.Selected = s.selectable(s.Selected)
	if s.SelectedClip != "" && !s.IsSelected(s.SelectedClip) {
		s.SelectedClip = ""
		if n := len(s.Selected); n > 0 {
			s.SelectedClip = s.Selected[n-1]
		}
	}
	if c, ti, _ := s.Project.Clip(s.SelectedClip); c != nil && before != nil {
		now := s.Project.Tracks[ti].ID
		if _, bti, _ := before.Clip(s.SelectedClip); bti >= 0 && bti < len(before.Tracks) {
			if was := before.Tracks[bti].ID; was != now && s.SelectedTrack == was {
				s.SelectedTrack = now
			}
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
	if len(s.Selected) != n0 || s.SelectedClip != clip0 || s.SelectedTrack != track0 || s.SelectedMedia != media0 {
		return ChangeSelection
	}
	return 0
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

// SelectClip makes id the only selected clip ("" clears the selection).
func (s *Session) SelectClip(id string) {
	if id == "" {
		s.SelectClips(nil)
		return
	}
	s.SelectClips([]string{id})
}

// selectable returns the IDs of ids that exist, without duplicates. When more than one
// clip remains, clips on locked tracks are left out: group edits (move, delete, the
// multi-clip inspector) would fail on them. A single clip on a locked track may be
// selected so the inspector can show it with a lock hint.
func (s *Session) selectable(ids []string) []string {
	var out, unlocked []string
	seen := map[string]bool{}
	for _, id := range ids {
		c, ti, _ := s.Project.Clip(id)
		if c == nil || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if !s.Project.Tracks[ti].Locked {
			unlocked = append(unlocked, id)
		}
	}
	if len(out) > 1 {
		return unlocked
	}
	return out
}

// SelectClips replaces the selection; the last ID becomes the primary clip. In a
// selection of several clips, clips on locked tracks are left out.
func (s *Session) SelectClips(ids []string) {
	sel := s.selectable(ids)
	primary := ""
	if len(sel) > 0 {
		primary = sel[len(sel)-1]
	}
	if primary == s.SelectedClip && equalIDs(sel, s.Selected) {
		return
	}
	s.Selected = sel
	s.SelectedClip = primary
	if c, ti, _ := s.Project.Clip(primary); c != nil {
		s.SelectedTrack = s.Project.Tracks[ti].ID
	}
	s.Notify(ChangeSelection)
}

// ToggleClip adds id to the selection or removes it (Ctrl+click).
func (s *Session) ToggleClip(id string) {
	if s.IsSelected(id) {
		var rest []string
		for _, x := range s.Selected {
			if x != id {
				rest = append(rest, x)
			}
		}
		s.SelectClips(rest)
		return
	}
	s.SelectClips(append(append([]string{}, s.Selected...), id))
}

func (s *Session) IsSelected(id string) bool {
	for _, x := range s.Selected {
		if x == id {
			return true
		}
	}
	return false
}

// SelectAll selects every clip on unlocked tracks (see SelectGroup for the primary clip).
func (s *Session) SelectAll() {
	var ids []string
	for _, t := range s.Project.Tracks {
		if t.Locked {
			continue
		}
		for _, c := range t.Clips {
			ids = append(ids, c.ID)
		}
	}
	s.SelectGroup(ids)
}

// SelectGroup replaces the selection with clips gathered in bulk (select all, rubber
// band). Unlike SelectClips the primary clip is not simply the last ID: the current
// primary stays primary when it is still part of the group, otherwise the first picture
// clip (video, image, title, colour) becomes primary, so the inspector and the colour page
// keep showing a gradeable clip. Only when the group holds nothing but sound is the first
// audio clip primary.
func (s *Session) SelectGroup(ids []string) {
	ids = s.selectable(ids)
	primary := ""
	for _, id := range ids {
		if id == s.SelectedClip {
			primary = id
		}
	}
	if primary == "" {
		for _, id := range ids {
			if c, _, _ := s.Project.Clip(id); c.Kind.IsVisual() {
				primary = id
				break
			}
		}
	}
	if primary == "" && len(ids) > 0 {
		primary = ids[0]
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != primary {
			out = append(out, id)
		}
	}
	if primary != "" {
		out = append(out, primary)
	}
	s.SelectClips(out)
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

// SetPlaying starts or stops normal-speed playback. Starting at the end rewinds to the start (or In).
func (s *Session) SetPlaying(on bool) {
	if !on {
		if s.Playing {
			s.Playing = false
			s.Rate = 1
			s.Notify(ChangePlayback)
		}
		return
	}
	if s.Playing && s.Rate == 1 {
		return
	}
	s.play(1)
}

func (s *Session) TogglePlaying() { s.SetPlaying(!s.Playing) }

// Shuttle implements J/K/L: dir > 0 plays forward and doubles the speed on repeated
// presses (up to 8x), dir < 0 does the same backwards, dir == 0 stops.
func (s *Session) Shuttle(dir int) {
	switch {
	case dir == 0:
		s.SetPlaying(false)
	case dir > 0 && s.Playing && s.Rate > 0:
		s.setRate(math.Min(s.Rate*2, 8))
	case dir < 0 && s.Playing && s.Rate < 0:
		s.setRate(math.Max(s.Rate*2, -8))
	case dir > 0:
		s.play(1)
	default:
		s.play(-1)
	}
}

func (s *Session) setRate(r float64) {
	s.Rate = r
	s.Notify(ChangePlayback)
}

func (s *Session) play(rate float64) {
	start, end := s.playStart(), s.playEnd()
	if end <= 0 {
		return
	}
	if rate > 0 && s.Playhead >= end {
		s.seek(start)
	}
	if rate < 0 && s.Playhead <= start {
		if !s.Loop {
			return
		}
		s.seek(end)
	}
	s.carry = 0
	s.Rate = rate
	s.Playing = true
	s.Notify(ChangePlayback)
}

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

// Advance moves the playhead by real time dt (scaled by Rate) while playing.
// Called by the playback clock.
func (s *Session) Advance(dt time.Duration) {
	if !s.Playing {
		return
	}
	frame := s.Project.FrameDuration()
	s.carry += time.Duration(float64(dt) * math.Abs(s.Rate))
	n := int(s.carry / frame)
	if n == 0 {
		return
	}
	s.carry -= time.Duration(n) * frame
	start, end := s.playStart(), s.playEnd()
	looping := s.Loop && end > start
	if s.Rate < 0 {
		f := s.Playhead - n
		if f < start {
			if !looping {
				s.seek(start)
				s.Playing, s.Rate = false, 1
				s.Notify(ChangePlayback)
				return
			}
			f = end - (start-f)%(end-start)
		}
		s.seek(f)
		return
	}
	f := s.Playhead + n
	if f >= end {
		if !looping {
			s.seek(end)
			s.Playing, s.Rate = false, 1
			s.Notify(ChangePlayback)
			return
		}
		f = start + (f-end)%(end-start)
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
