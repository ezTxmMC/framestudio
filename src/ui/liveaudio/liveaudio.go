// Package liveaudio plays the timeline mix while a session plays, on every page. When
// playback starts it mixes the program from the playhead with ffmpeg (export's audio
// mixer) and streams raw PCM into an external player (pw-play, paplay or ffplay). Pause,
// seeks, loop jumps and edits stop the stream; it restarts from the new playhead.
package liveaudio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
)

const (
	sampleRate = 48000
	channels   = 2
	// drift is how far (in seconds) the playhead may stray from the audio clock before
	// the stream restarts there, e.g. when loop playback jumps back to In.
	drift = 0.25
	// settle delays the restart after an edit, so a fader drag restarts the sound once.
	settle = 150 * time.Millisecond
)

// ErrNoPlayer is reported once when no audio player is installed.
var ErrNoPlayer = errors.New("Wiedergabe ohne Ton: Kein Audioplayer gefunden. PipeWire (pw-play), PulseAudio (paplay) oder ffmpeg (ffplay) installieren.")

// errNoFFmpeg is reported once when ffmpeg is missing.
var errNoFFmpeg = errors.New("Wiedergabe ohne Ton: ffmpeg wurde nicht gefunden. ffmpeg installieren, um beim Abspielen zu hören.")

// Stream plays the mix of p over [from,to) until ctx ends or the mix is over.
type Stream func(ctx context.Context, p *model.Project, from, to int) error

// driver ties one session's play state to a running audio stream. UI thread only,
// except the stream goroutines, which never touch the session.
type driver struct {
	s       *session.Session
	stream  Stream
	onError func(error)
	spawn   func(func()) // runs a stream in the background (tests run it inline)
	after   func(time.Duration, func()) func() bool

	cancel  context.CancelFunc
	started time.Time
	from    int
	held    int  // Hold calls in effect
	warned  bool // a missing tool was reported already
	pending func() bool
	wg      sync.WaitGroup
}

var (
	mu      sync.Mutex
	drivers = map[*session.Session]*driver{}
)

// Drive plays s's audio whenever s plays. onError receives problems once per session
// (missing player or ffmpeg); stop ends the sound and waits briefly for the player to quit.
func Drive(s *session.Session, onError func(error)) (stop func()) {
	return drive(s, PlayMix, onError, func(f func()) { go f() })
}

func drive(s *session.Session, stream Stream, onError func(error), spawn func(func())) (stop func()) {
	d := &driver{s: s, stream: stream, onError: onError, spawn: spawn,
		after: func(t time.Duration, f func()) func() bool { return time.AfterFunc(t, func() { kit.Do(f) }).Stop }}
	mu.Lock()
	drivers[s] = d
	mu.Unlock()
	unsub := s.Subscribe(d.changed)
	return func() {
		unsub()
		mu.Lock()
		if drivers[s] == d {
			delete(drivers, s)
		}
		mu.Unlock()
		d.stop()
		d.wait(2 * time.Second)
	}
}

// Hold mutes the live sound of s while another component plays it (the audio page's
// "Vorhören"); call release when done. UI thread only.
func Hold(s *session.Session) (release func()) {
	mu.Lock()
	d := drivers[s]
	mu.Unlock()
	if d == nil {
		return func() {}
	}
	d.held++
	d.stop()
	done := false
	return func() {
		if !done {
			done = true
			d.held--
		}
	}
}

func (d *driver) changed(c session.Change) {
	switch {
	case !d.s.Playing || d.held > 0:
		d.stop()
	case c&session.ChangeProject != 0:
		d.stop() // edits change the mix; restart once they settle
		d.schedule()
	case d.cancel == nil && d.pending == nil:
		d.start()
	case c&session.ChangePlayhead != 0 && d.cancel != nil && d.jumped():
		d.start()
	}
}

// jumped reports whether the playhead left the audio clock (seek or loop).
func (d *driver) jumped() bool {
	fps := d.s.FPS()
	if fps <= 0 {
		return false
	}
	want := float64(d.from) + time.Since(d.started).Seconds()*float64(fps)
	diff := float64(d.s.Playhead) - want
	return diff > drift*float64(fps) || diff < -drift*float64(fps)
}

func (d *driver) schedule() {
	if d.pending != nil {
		d.pending()
	}
	d.pending = d.after(settle, func() {
		d.pending = nil
		if d.s.Playing && d.held == 0 && d.cancel == nil {
			d.start()
		}
	})
}

// start (re)starts the stream at the playhead up to the end of playback.
func (d *driver) start() {
	d.stop()
	p := d.s.Project
	from := d.s.Playhead
	to := p.Duration()
	if d.s.HasRange() && from < d.s.Out {
		to = d.s.Out // playback stops (or loops) at Out
	}
	if to <= from || !export.HasAudio(p, from, to) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel, d.started, d.from = cancel, time.Now(), from
	clone := p.Clone()
	d.wg.Add(1)
	d.spawn(func() {
		defer d.wg.Done()
		err := d.stream(ctx, clone, from, to)
		if err != nil && ctx.Err() == nil {
			kit.Do(func() { d.report(err) })
		}
	})
}

func (d *driver) report(err error) {
	if d.warned || d.onError == nil {
		return
	}
	d.warned = true
	d.onError(err)
}

func (d *driver) stop() {
	if d.pending != nil {
		d.pending()
		d.pending = nil
	}
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
}

// wait blocks until the stream processes have exited or the timeout passed.
func (d *driver) wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// player is an external program that plays raw s16le stereo PCM from stdin.
type player struct {
	name string
	args []string
}

// players in order of preference: low latency first.
var players = []player{
	{"pw-play", []string{"--raw", "--format", "s16", "--rate", strconv.Itoa(sampleRate), "--channels", strconv.Itoa(channels), "--latency", "50ms", "-"}},
	{"paplay", []string{"--raw", "--format=s16le", "--rate=" + strconv.Itoa(sampleRate), "--channels=" + strconv.Itoa(channels), "--latency-msec=50"}},
	{"ffplay", []string{"-nodisp", "-autoexit", "-loglevel", "error", "-fflags", "nobuffer",
		"-f", "s16le", "-ar", strconv.Itoa(sampleRate), "-ch_layout", "stereo", "-i", "-"}},
}

var lookPath = exec.LookPath

func pickPlayer() (string, []string, error) {
	for _, p := range players {
		if path, err := lookPath(p.name); err == nil {
			return path, p.args, nil
		}
	}
	return "", nil, ErrNoPlayer
}

// PlayMix mixes p over [from,to) with ffmpeg and plays it until done or ctx ends. Media
// files that are missing are left out.
func PlayMix(ctx context.Context, p *model.Project, from, to int) error {
	ff, err := lookPath("ffmpeg")
	if err != nil {
		return errNoFFmpeg
	}
	play, playArgs, err := pickPlayer()
	if err != nil {
		return err
	}
	mix, ok := export.BuildAudioMix(p, from, to, 0, func(m model.MediaItem) bool {
		st, err := os.Stat(m.Path)
		return err == nil && !st.IsDir()
	})
	if !ok {
		return nil // nothing audible (e.g. all files offline)
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	for _, in := range mix.Inputs {
		args = append(args, "-i", in)
	}
	args = append(args, "-filter_complex", mix.Filter, "-map", "[aout]",
		"-f", "s16le", "-ar", strconv.Itoa(sampleRate), "-ac", strconv.Itoa(channels), "pipe:1")

	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	mixer := exec.CommandContext(ctx, ff, args...)
	mixer.Stdout = w
	out := exec.CommandContext(ctx, play, playArgs...)
	out.Stdin = r
	if err := out.Start(); err != nil {
		r.Close()
		w.Close()
		return fmt.Errorf("Wiedergabe ohne Ton: %s lässt sich nicht starten (%v)", play, err)
	}
	if err := mixer.Start(); err != nil {
		r.Close()
		w.Close()
		_ = out.Process.Kill()
		_ = out.Wait()
		return fmt.Errorf("Wiedergabe ohne Ton: ffmpeg lässt sich nicht starten (%v)", err)
	}
	r.Close() // the processes hold their own ends now
	w.Close()
	_ = mixer.Wait()
	_ = out.Wait() // the player drains its buffer, or is killed with ctx
	return nil
}
