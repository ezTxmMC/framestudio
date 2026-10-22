package liveaudio

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
)

type call struct {
	from, to int
	ctx      context.Context
}

// fixture drives a session whose A1 holds a 10 s tone, recording stream starts.
type fixture struct {
	s     *session.Session
	calls []call
	stop  func()
	timer func()
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	kit.UseUIQueue(t)
	p := model.NewProject("Ton")
	m := model.MediaItem{ID: "t", Name: "Ton", Path: "/x/ton.wav", Kind: model.KindAudio, Duration: 10 * time.Second}
	p.AddMedia(m)
	_ = p.PlaceClip(p.Tracks[2].ID, p.ClipFromMedia(m, 0, 0))
	f := &fixture{s: session.New(p)}
	stream := func(ctx context.Context, _ *model.Project, from, to int) error {
		f.calls = append(f.calls, call{from, to, ctx})
		return nil
	}
	f.stop = drive(f.s, stream, nil, func(fn func()) { fn() })
	mu.Lock()
	d := drivers[f.s]
	mu.Unlock()
	d.after = func(_ time.Duration, fn func()) func() bool {
		f.timer = fn
		return func() bool { f.timer = nil; return true }
	}
	t.Cleanup(f.stop)
	return f
}

func (f *fixture) last(t *testing.T) call {
	t.Helper()
	if len(f.calls) == 0 {
		t.Fatal("no stream started")
	}
	return f.calls[len(f.calls)-1]
}

func TestPlayStartsAndPauseStops(t *testing.T) {
	f := newFixture(t)
	f.s.Seek(50)
	f.s.SetPlaying(true)
	c := f.last(t)
	if c.from != 50 || c.to != 250 {
		t.Fatalf("stream %d..%d, want 50..250", c.from, c.to)
	}
	f.s.Advance(40 * time.Millisecond) // normal playback keeps the stream
	if len(f.calls) != 1 {
		t.Fatalf("%d streams after a playback tick", len(f.calls))
	}
	f.s.SetPlaying(false)
	if c.ctx.Err() == nil {
		t.Fatal("pause must stop the sound")
	}
}

func TestLoopJumpRestarts(t *testing.T) {
	f := newFixture(t)
	f.s.SetIn(25)
	f.s.SetOut(50)
	f.s.SetLoop(true)
	f.s.Seek(45)
	f.s.SetPlaying(true)
	first := f.last(t)
	if first.from != 45 || first.to != 50 {
		t.Fatalf("stream %d..%d, want 45..50", first.from, first.to)
	}
	f.s.Advance(280 * time.Millisecond) // 7 frames: wraps to 27
	if first.ctx.Err() == nil || f.last(t).from != 27 {
		t.Fatalf("loop jump must restart at In, streams %+v", f.calls)
	}
}

func TestEditRestartsAfterSettling(t *testing.T) {
	f := newFixture(t)
	f.s.SetPlaying(true)
	first := f.last(t)
	_ = f.s.Edit("Lautstärke", func(p *model.Project) error { p.Tracks[2].Volume = 0.5; return nil })
	if first.ctx.Err() == nil || len(f.calls) != 1 || f.timer == nil {
		t.Fatal("an edit stops the old mix and schedules a restart")
	}
	f.timer()
	if len(f.calls) != 2 {
		t.Fatal("restart after the edit settled")
	}
}

func TestHoldMutes(t *testing.T) {
	f := newFixture(t)
	release := Hold(f.s)
	f.s.SetPlaying(true)
	if len(f.calls) != 0 {
		t.Fatal("held session must stay silent")
	}
	release()
	f.s.Advance(40 * time.Millisecond)
	if len(f.calls) != 1 {
		t.Fatal("sound resumes after release")
	}
}

func TestSilentProjectStartsNothing(t *testing.T) {
	f := newFixture(t)
	_ = f.s.Edit("leeren", func(p *model.Project) error { p.Tracks[2].Muted = true; return nil })
	f.s.SetPlaying(true)
	if len(f.calls) != 0 {
		t.Fatal("muted audio must not start a stream")
	}
}

func TestNoPlayer(t *testing.T) {
	saved := lookPath
	defer func() { lookPath = saved }()
	lookPath = func(name string) (string, error) {
		if name == "ffmpeg" {
			return "/usr/bin/ffmpeg", nil
		}
		return "", context.Canceled
	}
	p := model.NewProject("x")
	if err := PlayMix(context.Background(), p, 0, 10); err != ErrNoPlayer {
		t.Fatalf("got %v", err)
	}
}

// TestPlayMixRuns plays 0.4 s of a quiet tone through the real tools when FS_LIVEAUDIO is set.
func TestPlayMixRuns(t *testing.T) {
	if os.Getenv("FS_LIVEAUDIO") == "" {
		t.Skip("set FS_LIVEAUDIO=1 to play through the sound card")
	}
	wav := filepath.Join(t.TempDir(), "ton.wav")
	if out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-af", "volume=0.05", wav).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	p := model.NewProject("x")
	m := model.MediaItem{ID: "t", Path: wav, Kind: model.KindAudio, Duration: time.Second}
	p.AddMedia(m)
	_ = p.PlaceClip(p.Tracks[2].ID, p.ClipFromMedia(m, 0, 0))
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := PlayMix(ctx, p, 0, 25); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancel did not stop the player")
	}
}
