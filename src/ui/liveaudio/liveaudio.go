// Package liveaudio plays the timeline mix while a session plays, on every page. It
// connects sessions to the shared low-latency engine in src/audio: each driven session
// is one voice that follows the playhead; media audio is decoded once into memory as
// soon as a project is driven, and one persistent player process (pw-play, paplay or
// ffplay) carries the sound of all sessions, so pressing Play is instant and in sync.
package liveaudio

import (
	"sync"
	"time"

	"framestudio-proto/src/audio"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
)

// ErrNoPlayer is reported once when no audio player is installed.
var ErrNoPlayer = audio.ErrNoPlayer

// driver ties one session's play state to an engine voice. UI thread only.
type driver struct {
	s       *session.Session
	e       *audio.Engine
	v       *audio.Voice
	onError func(error)
	held    int  // Hold calls in effect
	warned  bool // a problem was reported already
	stopped bool

	playing bool
	rate    float64
	gen     int
	frame   int // playhead last published
	scrub   int
}

var (
	mu      sync.Mutex
	drivers = map[*session.Session]*driver{}
	engine  *audio.Engine
	users   int
	// scrubbing plays a short snippet when the paused playhead moves.
	scrubbing = true
	// newEngine creates the shared engine when the first session is driven.
	newEngine = func() *audio.Engine { return audio.New(audio.Config{}) }
)

// UseEngine replaces how the shared engine is created, e.g. with a fake output in
// integration tests that must not open the sound card. Call it while no session is
// driven; restore puts the default back.
func UseEngine(create func() *audio.Engine) (restore func()) {
	mu.Lock()
	saved := newEngine
	newEngine = create
	mu.Unlock()
	return func() {
		mu.Lock()
		newEngine = saved
		mu.Unlock()
	}
}

// Drive plays s's audio whenever s plays and starts decoding its media right away.
// onError receives problems once per session (missing player or ffmpeg); stop ends the
// sound. Stopping the last driven session shuts the player down.
func Drive(s *session.Session, onError func(error)) (stop func()) {
	mu.Lock()
	if engine == nil {
		engine = newEngine()
		engine.Start()
	}
	users++
	e := engine
	mu.Unlock()

	d := &driver{s: s, e: e, onError: onError}
	d.v = e.AddVoice(func(err error) { kit.Do(func() { d.report(err) }) })
	d.v.SetProject(s.Project.Clone())
	d.publish()
	mu.Lock()
	drivers[s] = d
	mu.Unlock()
	unsub := s.Subscribe(d.changed)
	return func() {
		if d.stopped {
			return
		}
		d.stopped = true
		unsub()
		e.RemoveVoice(d.v)
		mu.Lock()
		if drivers[s] == d {
			delete(drivers, s)
		}
		users--
		last := users == 0 && engine == e
		if last {
			engine = nil
		}
		mu.Unlock()
		if last {
			e.Close()
		}
	}
}

// Active reports whether s is driven, i.e. its playback is sounded by the live engine.
// UI thread or any goroutine.
func Active(s *session.Session) bool { return lookup(s) != nil }

// Hold mutes the live sound of s while another component plays it (the audio page's
// "Vorhören"); call release when done. UI thread only.
func Hold(s *session.Session) (release func()) {
	d := lookup(s)
	if d == nil {
		return func() {}
	}
	d.held++
	d.publish()
	done := false
	return func() {
		if !done {
			done = true
			d.held--
			d.publish()
		}
	}
}

// Level is the recent output level of a track or the master bus, linear 0..1 per
// channel (L, R). Peak decays over ~300 ms; RMS covers ~50 ms.
type Level = audio.Level

// Levels returns the live levels of s as they are heard right now: the master bus and
// every audio track by ID (post-fader). Zero when silent or stopped. Safe to call from
// the UI thread at display rate.
func Levels(s *session.Session) (master Level, tracks map[string]Level) {
	d := lookup(s)
	if d == nil {
		return Level{}, nil
	}
	return d.v.Levels(d.e.Now())
}

// Latency is the engine's current output latency estimate (0 when no session is driven).
func Latency() time.Duration {
	mu.Lock()
	e := engine
	mu.Unlock()
	if e == nil {
		return 0
	}
	return e.Latency()
}

func lookup(s *session.Session) *driver {
	mu.Lock()
	defer mu.Unlock()
	return drivers[s]
}

// SetScrubbing turns the audio snippets on playhead moves while paused on or off
// (on by default). UI thread only.
func SetScrubbing(on bool) { scrubbing = on }

func (d *driver) changed(c session.Change) {
	if c&session.ChangeProject != 0 {
		d.v.SetProject(d.s.Project.Clone())
	}
	if c&session.ChangePlayhead != 0 && !d.s.Playing && d.s.Playhead != d.frame && scrubbing {
		d.scrub++ // dragged or stepped while paused: let it be heard briefly
	}
	if c&(session.ChangeProject|session.ChangePlayhead|session.ChangePlayback) != 0 {
		d.publish()
	}
}

// publish hands the session's play state to the engine.
func (d *driver) publish() {
	s := d.s
	exact := false
	if s.Playing && (!d.playing || s.Rate != d.rate) {
		d.gen++ // playback (re)started: the playhead sits exactly on its frame
		exact = true
	}
	d.playing, d.rate, d.frame = s.Playing, s.Rate, s.Playhead
	start, end := 0, s.Project.Duration()
	if s.HasRange() {
		start, end = s.In, s.Out
	}
	d.v.SetTransport(audio.Transport{
		Playing: s.Playing, Held: d.held > 0, Rate: s.Rate, Loop: s.Loop, FPS: s.FPS(),
		Frame: s.Playhead, At: d.e.Now(), Exact: exact, Gen: d.gen, Scrub: d.scrub, Start: start, End: end,
	})
}

func (d *driver) report(err error) {
	if d.warned || d.stopped || d.onError == nil {
		return
	}
	d.warned = true
	d.onError(err)
}
