package audio

import (
	"context"
	"errors"
	"io"
	"math"
	"runtime"
	"sync"
	"testing"
	"time"

	"framestudio-proto/src/model"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeOut is a device that consumes each block in exactly its duration of fake time.
type fakeOut struct {
	mu     sync.Mutex
	clk    *fakeClock
	blocks [][]int16
	closed bool
	fail   bool
}

func (o *fakeOut) Write(b []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.fail {
		return 0, errors.New("closed")
	}
	s := make([]int16, len(b)/2)
	for i := range s {
		s[i] = int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8)
	}
	o.blocks = append(o.blocks, s)
	if o.clk != nil {
		o.clk.Advance(time.Second * time.Duration(len(b)/bytesPerFrame) / SampleRate)
	}
	return len(b), nil
}

func (o *fakeOut) Latency() time.Duration { return 0 }

func (o *fakeOut) Close() error {
	o.mu.Lock()
	o.closed = true
	o.mu.Unlock()
	return nil
}

func (o *fakeOut) last() []int16 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.blocks[len(o.blocks)-1]
}

const testLatency = 50 * time.Millisecond

type rig struct {
	t   *testing.T
	clk *fakeClock
	out *fakeOut
	e   *Engine
	v   *Voice
	fm  *fakeMedia
	p   *model.Project
	tr  Transport
	t0  time.Time // when playback started
	f0  int
}

// newRig sets up an engine with a fake clock and device and a voice playing a
// project with a 10 s ramp clip at frame 0 of A1.
func newRig(t *testing.T) *rig {
	t.Helper()
	clk := &fakeClock{t: time.Unix(1000, 0)}
	out := &fakeOut{clk: clk}
	fm := newFakeMedia(10 * SampleRate)
	r := &rig{t: t, clk: clk, out: out, fm: fm}
	r.e = New(Config{Clock: clk, Open: func() (Output, error) { return out, nil },
		Cache: NewCache(fm.decode, fm.stat, 0), Latency: testLatency})
	t.Cleanup(r.e.Close)
	r.v = r.e.AddVoice(nil)
	r.p, _ = project(0, 250)
	r.setProject()
	return r
}

// setProject hands the voice a snapshot and waits until its audio is decoded.
func (r *rig) setProject() {
	r.t.Helper()
	before := r.v.next.Load()
	r.v.SetProject(r.p.Clone())
	deadline := time.Now().Add(5 * time.Second)
	for {
		pl := r.v.next.Load()
		if pl != nil && pl != before && decoded(pl) {
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatal("plan not ready")
		}
		time.Sleep(time.Millisecond)
	}
}

func decoded(pl *Plan) bool {
	for _, t := range pl.Tracks {
		for _, c := range t.Clips {
			if pcm, ok := c.Source.(*PCM); ok {
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

// play starts playback at frame f now, like session.SetPlaying.
func (r *rig) play(f int, rate float64) {
	r.f0, r.t0 = f, r.clk.Now()
	r.tr.Gen++
	r.tr = Transport{Playing: true, Rate: rate, FPS: model.Rate25, Frame: f, At: r.t0, Exact: true, Gen: r.tr.Gen, Start: 0, End: 250}
	r.v.SetTransport(r.tr)
}

// tick moves the playhead to where session.Advance would have it now.
func (r *rig) tick() {
	el := r.clk.Now().Sub(r.t0).Seconds() * 25 * r.tr.Rate
	r.tr.Frame = r.f0 + int(math.Floor(el))
	if r.tr.Rate < 0 {
		r.tr.Frame = r.f0 - int(math.Floor(-el))
	}
	r.tr.At, r.tr.Exact = r.clk.Now(), false
	r.v.SetTransport(r.tr)
}

func (r *rig) step(n int) {
	r.t.Helper()
	for range n {
		if err := r.e.Step(); err != nil {
			r.t.Fatal(err)
		}
	}
}

// pos decodes the ramp position from the left channel of sample i of the last block.
func (r *rig) pos(i int) int { return int(r.out.last()[2*i]) }

func silent(b []int16) bool {
	for _, s := range b {
		if s != 0 {
			return false
		}
	}
	return true
}

func TestIdleWritesSilence(t *testing.T) {
	r := newRig(t)
	r.step(3)
	if len(r.out.blocks) != 3 || !silent(r.out.last()) || len(r.out.last()) != 2*BlockFrames {
		t.Fatal("an idle engine streams silent 10 ms blocks")
	}
}

func TestPlayStartsInSyncAtOnce(t *testing.T) {
	r := newRig(t)
	r.step(2)
	r.play(50, 1)
	r.step(1)
	first := r.out.last()
	if silent(first) {
		t.Fatal("the first block after Play must sound")
	}
	// The block written now is heard after the latency: it must hold the program at
	// playhead + latency, then run on sample by sample.
	want := int(FramesToSamples(50, model.Rate25) + int64(testLatency.Seconds()*SampleRate))
	if first[2*(BlockFrames-1)+1] == 0 || first[1] > first[2*(BlockFrames-1)+1] {
		t.Fatal("the first block fades in")
	}
	r.tick()
	r.step(1)
	if got := r.pos(0); got != (want+BlockFrames)%16384 {
		t.Fatalf("second block starts at %d, want %d", got, (want+BlockFrames)%16384)
	}
	for i := 1; i < BlockFrames; i++ {
		if r.pos(i) != (r.pos(i-1)+1)%16384 {
			t.Fatalf("discontinuity at %d", i)
		}
	}
}

func TestContinuousPlaybackNoResync(t *testing.T) {
	r := newRig(t)
	r.play(10, 1)
	r.step(1)
	prev := -1
	for b := range 300 { // 3 s with the playhead ticking at ~60 Hz
		if b%2 == 0 {
			r.tick()
		}
		r.step(1)
		if prev >= 0 && r.pos(0) != (prev+1)%16384 {
			t.Fatalf("block %d jumped from %d to %d", b, prev, r.pos(0))
		}
		prev = r.pos(BlockFrames - 1)
	}
	if _, jumps := r.v.Stats(); jumps != 1 {
		t.Fatalf("%d resyncs during smooth playback, want only the start", jumps)
	}
}

func TestResyncOnJump(t *testing.T) {
	r := newRig(t)
	r.play(10, 1)
	r.step(5)
	// The playhead lags by a frame plus 30 ms of UI jitter: no jump.
	r.tick()
	r.tr.Frame--
	r.tr.At = r.tr.At.Add(30 * time.Millisecond)
	r.v.SetTransport(r.tr)
	r.step(1)
	if _, j := r.v.Stats(); j != 1 {
		t.Fatal("jitter within tolerance must not resync")
	}
	// An edit moved the playhead 2 s ahead (e.g. ripple): jump there.
	r.f0, r.t0 = r.tr.Frame+50, r.clk.Now()
	r.tick()
	r.step(2)
	if _, j := r.v.Stats(); j != 2 {
		t.Fatal("a 2 s jump must resync")
	}
	want := int(FramesToSamples(r.f0, model.Rate25)) + int((r.clk.Now().Sub(r.t0)-10*time.Millisecond+testLatency).Seconds()*SampleRate)
	if got := r.pos(0); math.Abs(float64(got-want%16384)) > 1920/2+2 {
		t.Fatalf("after the jump at %d, want ~%d", got, want%16384)
	}
}

func TestLoopWrapsWithoutRestart(t *testing.T) {
	r := newRig(t)
	r.play(20, 1)
	r.tr.Loop, r.tr.Start, r.tr.End = true, 10, 30
	r.v.SetTransport(r.tr)
	r.step(1)
	// 0.4 s range: the sound wraps by itself when the extrapolated playhead passes Out.
	wrapped := false
	for range 60 {
		el := r.clk.Now().Sub(r.t0).Seconds() * 25
		f := 20 + int(el)
		if f >= 30 {
			f = 10 + (f-30)%20
		}
		r.tr.Frame, r.tr.At, r.tr.Exact = f, r.clk.Now(), false
		r.v.SetTransport(r.tr)
		r.step(1)
		for i := range BlockFrames {
			p := r.pos(i)
			if p > int(FramesToSamples(30, model.Rate25))+4 {
				t.Fatalf("sound past Out: %d", p)
			}
			if p < int(FramesToSamples(15, model.Rate25)) && p > 0 {
				wrapped = true
			}
		}
	}
	_, jumps := r.v.Stats()
	if !wrapped || jumps > 4 {
		t.Fatalf("wrapped %v, %d resyncs for 1.5 loop passes", wrapped, jumps)
	}
}

func TestEditSwapsPlanSmoothly(t *testing.T) {
	r := newRig(t)
	r.play(0, 1)
	r.step(3)
	r.p.Tracks[2].Volume = 0.5
	r.setProject()
	r.tick()
	r.step(1) // crossfade block
	b := r.out.last()
	if b[1] < 4096-50 || b[2*(BlockFrames-1)+1] > 4096+50 {
		t.Fatalf("the right channel should glide from 8192 to 4096: %d .. %d", b[1], b[2*(BlockFrames-1)+1])
	}
	r.tick()
	r.step(1)
	if r.out.last()[1] != 4096 {
		t.Fatalf("new volume after the swap: %d", r.out.last()[1])
	}
	if _, j := r.v.Stats(); j != 1 {
		t.Fatal("an edit must not restart playback")
	}
}

func TestStopFadesOutAndHoldMutes(t *testing.T) {
	r := newRig(t)
	r.play(0, 1)
	r.step(3)
	r.tr.Playing = false
	r.v.SetTransport(r.tr)
	r.step(1)
	b := r.out.last()
	if b[1] == 0 || b[2*BlockFrames-1] > 100 {
		t.Fatal("pause fades out within one block")
	}
	r.step(1)
	if !silent(r.out.last()) {
		t.Fatal("silence after pause")
	}
	r.play(0, 1)
	r.tr.Held = true
	r.v.SetTransport(r.tr)
	r.step(2)
	if !silent(r.out.last()) {
		t.Fatal("a held voice is silent")
	}
}

func TestScrubSnippet(t *testing.T) {
	r := newRig(t)
	r.step(1)
	r.tr = Transport{FPS: model.Rate25, Frame: 100, At: r.clk.Now(), Scrub: 1, End: 250}
	r.v.SetTransport(r.tr)
	r.step(1)
	if r.pos(BlockFrames-1) == 0 || r.out.last()[1] > 2000 {
		t.Fatal("the snippet fades in")
	}
	r.step(1)
	if want := int(FramesToSamples(100, model.Rate25)+BlockFrames) % 16384; r.pos(0) != want {
		t.Fatalf("snippet at %d, want %d (no latency offset while paused)", r.pos(0), want)
	}
	r.step(4) // 4 more blocks, the last fading out: 60 ms in all
	if silent(r.out.last()) {
		t.Fatal("the snippet fades out")
	}
	r.step(1)
	if !silent(r.out.last()) {
		t.Fatal("the snippet lasts ~60 ms")
	}
	r.play(0, 1)
	r.tr.Held = true
	r.tr.Scrub = 2
	r.v.SetTransport(r.tr)
	r.step(1)
	r.tr.Playing = false
	r.v.SetTransport(r.tr)
	r.step(2)
	if !silent(r.out.last()) {
		t.Fatal("no snippet for a held voice or a move during playback")
	}
}

func TestShuttleRates(t *testing.T) {
	r := newRig(t)
	r.play(100, -1)
	r.step(2)
	if r.pos(1) != r.pos(0)-1 {
		t.Fatal("J plays backwards")
	}
	r.play(100, 2)
	r.step(2)
	if d := r.pos(1) - r.pos(0); d != 2 {
		t.Fatalf("2x advances 2 samples per sample, got %d", d)
	}
	r.play(100, 4)
	r.step(2)
	if !silent(r.out.last()) {
		t.Fatal("4x is silent")
	}
}

func TestLevels(t *testing.T) {
	r := newRig(t)
	r.p.Tracks[2].Clips[0].Volume = 4 // 0.25 * 4 = 1.0: clips
	r.setProject()
	r.play(0, 1)
	r.step(1)
	m, _ := r.v.Levels(r.clk.Now())
	if m.Peak != [2]float32{} {
		t.Fatalf("levels must wait for the latency, got %v", m.Peak)
	}
	r.step(19)
	now := r.clk.Now()
	m, tracks := r.v.Levels(now)
	a1 := tracks[r.p.Tracks[2].ID]
	if m.Peak[1] < 0.9 || !m.Clipped || !a1.Clipped || math.Abs(float64(a1.RMS[1])-1) > 0.01 || len(tracks) != 1 {
		t.Fatalf("master %+v A1 %+v", m, a1)
	}
	r.tr.Playing = false
	r.v.SetTransport(r.tr)
	r.step(100)
	later := now.Add(testLatency + 10*time.Millisecond + 300*time.Millisecond) // 300 ms after the last sound was heard
	m, _ = r.v.Levels(later)
	if m.RMS[1] != 0 || m.Peak[1] > 0.4 || m.Peak[1] < 0.3 || !m.Clipped {
		t.Fatalf("after 300 ms: %+v", m)
	}
	m, _ = r.v.Levels(now.Add(1100 * time.Millisecond))
	if m.Clipped {
		t.Fatal("clip indicator holds about 1 s")
	}
	m, _ = r.v.Levels(now.Add(3 * time.Second))
	if m != (Level{}) {
		t.Fatalf("silence after 3 s: %+v", m)
	}
}

func TestErrorsReportedOnce(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	opens := 0
	e := New(Config{Clock: clk, Open: func() (Output, error) { opens++; return nil, ErrNoPlayer },
		Cache: NewCache(newFakeMedia(SampleRate).decode, newFakeMedia(0).stat, 0)})
	defer e.Close()
	var errs []error
	v := e.AddVoice(func(err error) { errs = append(errs, err) })
	p, _ := project(0, 25)
	v.SetProject(p)
	for v.next.Load() == nil {
		time.Sleep(time.Millisecond)
	}
	e.mu.Lock()
	e.out = &fakeOut{clk: clk} // stands in for the real-time null output
	e.isNull, e.outErr = true, ErrNoPlayer
	e.mu.Unlock()
	if err := e.Step(); err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatal("no report while nothing plays")
	}
	v.SetTransport(Transport{Playing: true, Rate: 1, FPS: model.Rate25, At: clk.Now(), Gen: 1, End: 25, Exact: true})
	for range 3 {
		_ = e.Step()
	}
	if len(errs) != 1 || errs[0] != ErrNoPlayer {
		t.Fatalf("reports %v", errs)
	}
}

func TestNoFFmpegReported(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	fm := newFakeMedia(0)
	c := NewCache(func(context.Context, string) (io.ReadCloser, error) { return nil, ErrNoFFmpeg }, fm.stat, 0)
	e := New(Config{Clock: clk, Open: func() (Output, error) { return &fakeOut{clk: clk}, nil }, Cache: c})
	defer e.Close()
	var errs []error
	v := e.AddVoice(func(err error) { errs = append(errs, err) })
	p, _ := project(0, 25)
	v.SetProject(p)
	for v.next.Load() == nil || !decoded(v.next.Load()) {
		time.Sleep(time.Millisecond)
	}
	v.SetTransport(Transport{Playing: true, Rate: 1, FPS: model.Rate25, At: clk.Now(), Gen: 1, End: 25, Exact: true})
	for range 3 {
		_ = e.Step()
	}
	if len(errs) != 1 || errs[0] != ErrNoFFmpeg {
		t.Fatalf("reports %v", errs)
	}
}

func TestRelinkDropsOldAudio(t *testing.T) {
	r := newRig(t)
	old := r.e.Cache().Keys()
	r.p.Media[0].Path = "/relinked.wav"
	r.setProject()
	keys := r.e.Cache().Keys()
	if len(old) != 1 || len(keys) != 1 || keys[0] == old[0] {
		t.Fatalf("keys before %q after %q", old, keys)
	}
}

func TestOutputRestartsAfterPlayerQuit(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	var outs []*fakeOut
	e := New(Config{Clock: clk, Open: func() (Output, error) {
		o := &fakeOut{clk: clk}
		outs = append(outs, o)
		return o, nil
	}, Cache: NewCache(newFakeMedia(0).decode, newFakeMedia(0).stat, 0)})
	defer e.Close()
	_ = e.Step()
	clk.Advance(2 * time.Second)
	outs[0].fail = true
	if err := e.Step(); err == nil {
		t.Fatal("write error expected")
	}
	if err := e.Step(); err != nil || len(outs) != 2 || !outs[0].closed {
		t.Fatalf("player restarted: %v, %d opens", err, len(outs))
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for range 3 {
		fm := newFakeMedia(5 * SampleRate)
		out := &fakeOut{}
		e := New(Config{Open: func() (Output, error) { return slowOut{out}, nil }, Cache: NewCache(fm.decode, fm.stat, 0)})
		e.Start()
		v := e.AddVoice(nil)
		p, _ := project(0, 100)
		v.SetProject(p)
		v.SetTransport(Transport{Playing: true, Rate: 1, FPS: model.Rate25, At: time.Now(), Gen: 1, End: 100, Exact: true})
		time.Sleep(30 * time.Millisecond)
		e.RemoveVoice(v)
		e.Close()
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

// slowOut paces like a real device.
type slowOut struct{ *fakeOut }

func (o slowOut) Write(b []byte) (int, error) {
	time.Sleep(time.Second * time.Duration(len(b)/bytesPerFrame) / SampleRate)
	return o.fakeOut.Write(b)
}
