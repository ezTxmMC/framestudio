package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/liveaudio"
)

// previewSeconds limits how much audio one "Vorhören" mixes.
const previewSeconds = 30

var (
	errNoPlayer = errors.New("Kein Audioplayer gefunden. ffmpeg (ffplay) oder PipeWire (pw-play) installieren.")
	errNoAudio  = errors.New("Auf den Audiospuren liegen keine Clips. Erst Audio auf der Zeitleiste platzieren.")
)

// playerCmd is an external program that plays a WAV file.
type playerCmd struct {
	Name string // program name shown to the user
	Path string // resolved executable
	Args []string
}

// pickPlayer returns the first installed player for file: ffplay, then pw-play, then paplay.
func pickPlayer(lookPath func(string) (string, error), file string) (playerCmd, error) {
	candidates := []playerCmd{
		{Name: "ffplay", Args: []string{"-nodisp", "-autoexit", "-loglevel", "error", file}},
		{Name: "pw-play", Args: []string{file}},
		{Name: "paplay", Args: []string{file}},
	}
	for _, c := range candidates {
		if path, err := lookPath(c.Name); err == nil && path != "" {
			c.Path = path
			return c, nil
		}
	}
	return playerCmd{}, errNoPlayer
}

// previewRange returns the frames [from, to) to preview: the In/Out range if set, otherwise
// from the playhead (or the start, if the playhead is past the end), at most previewSeconds long.
func previewRange(playhead, in, out, duration, fps int) (from, to int, err error) {
	if duration <= 0 {
		return 0, 0, errNoAudio
	}
	if fps <= 0 {
		fps = 25
	}
	if out > in {
		from, to = in, min(out, duration)
		if from >= to {
			return 0, 0, errors.New("Der In/Out-Bereich liegt hinter dem Programmende. Bereich neu setzen.")
		}
	} else {
		from, to = playhead, duration
		if from >= duration {
			from = 0
		}
	}
	return from, min(to, from+previewSeconds*fps), nil
}

// hasAudioClips reports whether any audio track holds a clip.
func hasAudioClips(p *model.Project) bool {
	for _, ti := range p.TrackIndices(model.TrackAudio) {
		if len(p.Tracks[ti].Clips) > 0 {
			return true
		}
	}
	return false
}

type previewState int

const (
	previewIdle previewState = iota
	previewMixing
	previewPlaying
)

// previewer mixes the timeline audio to a temporary WAV and plays it with an external
// player while the session playhead runs in sync. All methods run on the UI thread.
type previewer struct {
	s        *session.Session
	run      func(ctx context.Context, p *model.Project, o export.Options) error
	lookPath func(string) (string, error)
	spawn    func(func()) // runs background work
	onChange func()       // state or status changed
	onError  func(error)

	state   previewState
	status  string
	gen     int
	cancel  context.CancelFunc
	from    int
	syncing bool   // the previewer itself is changing session playback
	release func() // lifts the hold on the live playback sound
	wg      sync.WaitGroup
}

// wait blocks until mixdowns and players have exited (temp file removed) or the
// timeout passed.
func (v *previewer) wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		v.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Busy reports whether a preview is being mixed or played.
func (v *previewer) Busy() bool { return v.state != previewIdle }

// Start mixes and plays the preview range, replacing any running preview.
func (v *previewer) Start() {
	v.Stop()
	p := v.s.Project
	if !hasAudioClips(p) {
		v.onError(errNoAudio)
		return
	}
	in, out := v.s.In, v.s.Out
	if !v.s.HasRange() {
		in, out = 0, 0
	}
	from, to, err := previewRange(v.s.Playhead, in, out, p.Duration(), p.Settings.FPS)
	if err != nil {
		v.onError(err)
		return
	}
	player, err := pickPlayer(v.lookPath, "")
	if err != nil {
		v.onError(err)
		return
	}
	f, err := os.CreateTemp("", "framestudio-vorhoeren-*.wav")
	if err != nil {
		v.onError(fmt.Errorf("Temporäre Datei für das Vorhören lässt sich nicht anlegen: %w", err))
		return
	}
	path := f.Name()
	_ = f.Close()
	player.Args[len(player.Args)-1] = path

	ctx, cancel := context.WithCancel(context.Background())
	v.gen++
	gen := v.gen
	v.cancel, v.state, v.from = cancel, previewMixing, from
	v.status = "Audio wird gemischt …"
	v.onChange()

	opts := export.Options{
		Format: export.FormatWAV, Path: path, Quality: export.QualityHigh,
		Width: p.Settings.Width, Height: p.Settings.Height,
		From: from, To: to, IncludeAudio: true,
	}
	clone := p.Clone()
	v.wg.Add(1)
	v.spawn(func() {
		defer v.wg.Done()
		defer os.Remove(path)
		if err := v.run(ctx, clone, opts); err != nil {
			kit.Do(func() {
				v.failed(gen, fmt.Errorf("Audio für das Vorhören ließ sich nicht mischen: %w", err))
			})
			return
		}
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, player.Path, player.Args...)
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			kit.Do(func() { v.failed(gen, fmt.Errorf("%s lässt sich nicht starten: %w", player.Name, err)) })
			return
		}
		kit.Do(func() { v.playing(gen, player.Name) })
		err := cmd.Wait()
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
				msg = msg[i+1:]
			}
			if msg == "" {
				msg = err.Error()
			}
			err = fmt.Errorf("Wiedergabe mit %s abgebrochen (%s). Audioausgabe des Systems prüfen.", player.Name, msg)
		}
		kit.Do(func() { v.failed(gen, err) })
	})
}

// Stop kills the player or cancels the mixdown and stops session playback.
func (v *previewer) Stop() {
	if v.state == previewIdle {
		return
	}
	v.finish()
}

func (v *previewer) finish() {
	v.gen++
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	v.state, v.status = previewIdle, ""
	if v.s.Playing {
		v.syncing = true
		v.s.SetPlaying(false)
		v.syncing = false
	}
	if v.release != nil {
		v.release()
		v.release = nil
	}
	v.onChange()
}

// playing starts session playback once the player runs.
func (v *previewer) playing(gen int, name string) {
	if gen != v.gen {
		return
	}
	v.state = previewPlaying
	v.status = "Wiedergabe über " + filepath.Base(name)
	if v.release == nil {
		v.release = liveaudio.Hold(v.s) // the preview player makes the sound itself
	}
	v.syncing = true
	v.s.Seek(v.from)
	v.s.SetPlaying(true)
	v.syncing = false
	v.onChange()
}

// failed ends the preview of generation gen, reporting err unless the user stopped it.
func (v *previewer) failed(gen int, err error) {
	if gen != v.gen {
		return
	}
	v.finish()
	if err != nil && !errors.Is(err, context.Canceled) {
		v.onError(err)
	}
}

// sessionChanged stops the player when playback was stopped elsewhere (Seek, transport).
func (v *previewer) sessionChanged(c session.Change) {
	if c&session.ChangePlayback != 0 && v.state == previewPlaying && !v.syncing && !v.s.Playing {
		v.finish()
	}
}
