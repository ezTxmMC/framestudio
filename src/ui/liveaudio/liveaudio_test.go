package liveaudio

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"framestudio-proto/src/audio"
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
)

// toneReader streams n frames of a constant 0.25 / 0.25 signal.
type toneReader struct{ n int }

func (r *toneReader) Read(b []byte) (int, error) {
	if r.n == 0 {
		return 0, io.EOF
	}
	w := 0
	for ; w+4 <= len(b) && r.n > 0; w, r.n = w+4, r.n-1 {
		b[w], b[w+1], b[w+2], b[w+3] = 0, 0x20, 0, 0x20
	}
	return w, nil
}

func (r *toneReader) Close() error { return nil }

func toneDecoder(context.Context, string) (io.ReadCloser, error) {
	return &toneReader{n: 10 * audio.SampleRate}, nil
}

// device is an output that consumes audio in real time, like a sound card, and
// remembers when the first sound arrived.
type device struct {
	mu     sync.Mutex
	writes int
	first  time.Time // first non-silent block written
	sound  bool      // the last block had sound
	closed bool
}

func (d *device) Write(b []byte) (int, error) {
	defer time.Sleep(time.Second * time.Duration(len(b)/4) / audio.SampleRate) // played in real time
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, io.ErrClosedPipe
	}
	d.writes++
	d.sound = false
	for _, x := range b {
		if x != 0 {
			d.sound = true
			break
		}
	}
	if d.sound && d.first.IsZero() {
		d.first = time.Now()
	}
	return len(b), nil
}

func (d *device) Latency() time.Duration { return 40 * time.Millisecond }

func (d *device) Close() error {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	return nil
}

func (d *device) state() (sound bool, first time.Time, closed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sound, d.first, d.closed
}

type fixture struct {
	q    *kit.UIQueue
	s    *session.Session
	dev  *device
	stop func()
	errs []error
}

// newFixture drives a session whose A1 holds a 10 s tone through a real-time fake device.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{q: kit.UseUIQueue(t), dev: &device{}}
	wav := filepath.Join(t.TempDir(), "ton.wav")
	if err := os.WriteFile(wav, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := newEngine
	newEngine = func() *audio.Engine {
		return audio.New(audio.Config{Open: func() (audio.Output, error) { return f.dev, nil },
			Cache: audio.NewCache(toneDecoder, nil, 0)})
	}
	t.Cleanup(func() { newEngine = saved })
	p := model.NewProject("Ton")
	m := model.MediaItem{ID: "t", Name: "Ton", Path: wav, Kind: model.KindAudio, Duration: 10 * time.Second}
	p.AddMedia(m)
	_ = p.PlaceClip(p.Tracks[2].ID, p.ClipFromMedia(m, 0, 0))
	f.s = session.New(p)
	f.stop = Drive(f.s, func(err error) { f.errs = append(f.errs, err) })
	t.Cleanup(f.stop)
	if !f.q.Until(func() bool { return ready(f.s) }, 3*time.Second) {
		t.Fatal("audio not prepared")
	}
	return f
}

// ready reports whether the session's audio is decoded.
func ready(s *session.Session) bool {
	d := lookup(s)
	pl := d.v.Plan()
	if pl == nil {
		return false
	}
	for _, t := range pl.Tracks {
		for _, c := range t.Clips {
			if pcm, ok := c.Source.(*audio.PCM); ok {
				select {
				case <-pcm.Done():
				default:
					return false
				}
			}
		}
	}
	return true
}

// within waits for cond on the real-time device.
func (f *fixture) within(t *testing.T, what string, cond func() bool) {
	t.Helper()
	if !f.q.Until(cond, 2*time.Second) {
		t.Fatal(what)
	}
}

func (f *fixture) sounding() bool { s, _, _ := f.dev.state(); return s }

func TestPlaySoundsAndPauseStops(t *testing.T) {
	f := newFixture(t)
	time.Sleep(50 * time.Millisecond)
	if f.sounding() {
		t.Fatal("silent before Play")
	}
	f.s.SetPlaying(true)
	f.within(t, "Play must sound", f.sounding)
	m, tracks := Levels(f.s)
	f.within(t, "meters show the sound", func() bool { m, tracks = Levels(f.s); return m.RMS[0] > 0.2 })
	if a1 := tracks[f.s.Project.Tracks[2].ID]; a1.RMS[0] < 0.2 || a1.Clipped {
		t.Fatalf("A1 level %+v", a1)
	}
	f.s.SetPlaying(false)
	f.within(t, "pause must stop the sound", func() bool { return !f.sounding() })
}

func TestEditKeepsPlaying(t *testing.T) {
	f := newFixture(t)
	f.s.SetPlaying(true)
	f.within(t, "Play must sound", f.sounding)
	d := lookup(f.s)
	_, jumps := d.v.Stats()
	_ = f.s.Edit("Lautstärke", func(p *model.Project) error { p.Tracks[2].Volume = 0.5; return nil })
	f.within(t, "the edit reaches the mix", func() bool {
		m, _ := Levels(f.s)
		return m.Peak[0] > 0.1 && m.Peak[0] < 0.14
	})
	if _, j := d.v.Stats(); j != jumps {
		t.Fatal("an edit must not restart the sound")
	}
	_ = f.s.Edit("leeren", func(p *model.Project) error { p.Tracks[2].Muted = true; return nil })
	f.within(t, "muting silences", func() bool { return !f.sounding() })
}

func TestScrubSnippet(t *testing.T) {
	f := newFixture(t)
	f.s.Seek(100)
	f.within(t, "a paused seek plays a snippet", f.sounding)
	f.within(t, "the snippet ends by itself", func() bool { return !f.sounding() })
	d := lookup(f.s)
	blocks, _ := d.v.Stats()
	if blocks < 5 || blocks > 7 {
		t.Fatalf("snippet of %d blocks, want ~60 ms", blocks)
	}
	SetScrubbing(false)
	defer SetScrubbing(true)
	f.s.Seek(10)
	time.Sleep(60 * time.Millisecond)
	if b, _ := d.v.Stats(); b != blocks {
		t.Fatal("scrubbing off stays silent")
	}
}

func TestHoldMutes(t *testing.T) {
	f := newFixture(t)
	release := Hold(f.s)
	f.s.SetPlaying(true)
	time.Sleep(80 * time.Millisecond)
	if f.sounding() {
		t.Fatal("held session must stay silent")
	}
	release()
	release() // twice is harmless
	f.within(t, "sound resumes after release", f.sounding)
}

func TestLastStopClosesPlayer(t *testing.T) {
	f := newFixture(t)
	other := session.New(model.NewProject("Zwei"))
	stop2 := Drive(other, nil)
	mu.Lock()
	n := users
	mu.Unlock()
	if n != 2 {
		t.Fatalf("%d users", n)
	}
	stop2()
	if _, _, closed := f.dev.state(); closed {
		t.Fatal("the player stays while a project is open")
	}
	f.stop()
	f.stop() // idempotent
	if _, _, closed := f.dev.state(); !closed {
		t.Fatal("stopping the last session closes the player")
	}
	if Latency() != 0 {
		t.Fatal("no engine left")
	}
	m, tracks := Levels(f.s)
	if m != (Level{}) || tracks != nil {
		t.Fatal("levels of an undriven session are zero")
	}
}

func TestNoPlayerReportedOnce(t *testing.T) {
	q := kit.UseUIQueue(t)
	saved := newEngine
	defer func() { newEngine = saved }()
	newEngine = func() *audio.Engine {
		return audio.New(audio.Config{Open: func() (audio.Output, error) { return nil, ErrNoPlayer },
			Cache: audio.NewCache(toneDecoder, nil, 0)})
	}
	wav := filepath.Join(t.TempDir(), "ton.wav")
	_ = os.WriteFile(wav, []byte("x"), 0o644)
	p := model.NewProject("x")
	m := model.MediaItem{ID: "t", Path: wav, Kind: model.KindAudio, Duration: time.Second}
	p.AddMedia(m)
	_ = p.PlaceClip(p.Tracks[2].ID, p.ClipFromMedia(m, 0, 0))
	s := session.New(p)
	var errs []error
	stop := Drive(s, func(err error) { errs = append(errs, err) })
	defer stop()
	q.Until(func() bool { return ready(s) }, 2*time.Second)
	q.Settle(50*time.Millisecond, time.Second)
	if len(errs) != 0 {
		t.Fatal("no report before Play")
	}
	s.SetPlaying(true)
	q.Until(func() bool { return len(errs) > 0 }, 2*time.Second)
	s.SetPlaying(false)
	s.SetPlaying(true)
	q.Settle(100*time.Millisecond, time.Second)
	if len(errs) != 1 || errs[0] != ErrNoPlayer {
		t.Fatalf("reports %v", errs)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for range 2 {
		f := newFixture(t)
		f.s.SetPlaying(true)
		f.within(t, "sound", f.sounding)
		f.stop()
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		buf := make([]byte, 1<<16)
		t.Fatalf("%d goroutines before, %d after\n%s", before, n, buf[:runtime.Stack(buf, true)])
	}
}

// TestStartLatency measures the time from SetPlaying(true) to the first sound written
// to a device that consumes audio in real time.
func TestStartLatency(t *testing.T) {
	var worst time.Duration
	for range 5 {
		f := newFixture(t)
		time.Sleep(30 * time.Millisecond) // idle stream running, as in the app
		start := time.Now()
		f.s.SetPlaying(true)
		f.within(t, "sound", f.sounding)
		_, first, _ := f.dev.state()
		worst = max(worst, first.Sub(start))
		f.stop()
	}
	t.Logf("SetPlaying(true) to first non-silent sample written: <= %v (plus the device latency)", worst)
	if worst > 30*time.Millisecond {
		t.Fatalf("start latency %v", worst)
	}
}

// TestRealPlayer plays 0.4 s of a quiet tone through the sound card when FS_LIVEAUDIO
// is set, and reports the start latency.
func TestRealPlayer(t *testing.T) {
	if os.Getenv("FS_LIVEAUDIO") == "" {
		t.Skip("set FS_LIVEAUDIO=1 to play through the sound card")
	}
	q := kit.UseUIQueue(t)
	wav := filepath.Join(t.TempDir(), "ton.wav")
	if out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-af", "volume=0.05", wav).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	var first time.Time
	var fmu sync.Mutex
	saved := newEngine
	defer func() { newEngine = saved }()
	newEngine = func() *audio.Engine {
		return audio.New(audio.Config{OnWrite: func(at time.Time, b []byte) {
			fmu.Lock()
			defer fmu.Unlock()
			if first.IsZero() {
				for _, x := range b {
					if x != 0 {
						first = time.Now()
						break
					}
				}
			}
		}})
	}
	p := model.NewProject("x")
	m := model.MediaItem{ID: "t", Path: wav, Kind: model.KindAudio, Duration: time.Second}
	p.AddMedia(m)
	_ = p.PlaceClip(p.Tracks[2].ID, p.ClipFromMedia(m, 0, 0))
	s := session.New(p)
	var errs []error
	stop := Drive(s, func(err error) { errs = append(errs, err) })
	stopClock := kit.DrivePlayback(s)
	q.Until(func() bool { return ready(s) }, 3*time.Second)
	time.Sleep(300 * time.Millisecond) // player connected
	start := time.Now()
	s.SetPlaying(true)
	q.Until(func() bool { return false }, 400*time.Millisecond)
	stopClock()
	lat := Latency()
	t0 := time.Now()
	stop()
	fmu.Lock()
	defer fmu.Unlock()
	t.Logf("first sound written %v after Play; output latency estimate %v; shutdown %v; errors %v",
		first.Sub(start), lat, time.Since(t0), errs)
	if first.IsZero() || len(errs) > 0 {
		t.Fatal("no sound")
	}
}
