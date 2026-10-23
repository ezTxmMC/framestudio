package audio

import (
	"errors"
	"math"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"framestudio-proto/src/model"
)

const (
	// BlockFrames is the mixer block: 10 ms. Swaps, jumps, starts and stops crossfade
	// over one block.
	BlockFrames = SampleRate / 100
	// resyncTolerance is how far the sound may drift from the picture before it jumps.
	resyncTolerance = 40 * time.Millisecond
	// scrubBlocks is the length of a scrub snippet in blocks (plus a fade-out block).
	scrubBlocks = 5
	// retryOutput is the pause before a failed player is started again.
	retryOutput = 3 * time.Second
)

// Clock tells the engine the time; tests use a fake one.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Config sets up an Engine. Zero values pick the production defaults.
type Config struct {
	Clock   Clock
	Open    func() (Output, error) // nil = OpenPlayer
	Cache   *Cache                 // nil = a new ffmpeg-backed cache; closed with the engine
	Latency time.Duration          // fixed output latency; 0 = the output's estimate or FS_AUDIO_LATENCY_MS
	// OnWrite, if set, sees every block right before it is written (tests, measurements).
	OnWrite func(at time.Time, block []byte)
}

// Engine mixes all voices into one persistent output. Create with New, call Start for
// the background loop (tests may call Step instead) and Close when done.
type Engine struct {
	clock    Clock
	open     func() (Output, error)
	cache    *Cache
	fixedLat time.Duration
	onWrite  func(time.Time, []byte)

	mu       sync.Mutex
	voices   []*Voice
	out      Output
	isNull   bool
	openedAt time.Time
	retryAt  time.Time
	outErr   error
	closed   bool
	started  bool

	prep    chan *Voice
	removed chan *Voice // voices to release (keys, decodes), handled by the prep loop
	quit    chan struct{}
	wg      sync.WaitGroup

	// mixer scratch, loop goroutine only
	mix  []float32
	pcm  []byte
	keys map[*Voice]map[string]string // prep goroutine only: media keys in use
}

// New creates an idle engine.
func New(cfg Config) *Engine {
	e := &Engine{clock: cfg.Clock, open: cfg.Open, cache: cfg.Cache, fixedLat: cfg.Latency, onWrite: cfg.OnWrite,
		prep: make(chan *Voice, 64), removed: make(chan *Voice, 16), quit: make(chan struct{}), keys: map[*Voice]map[string]string{},
		mix: make([]float32, 2*BlockFrames), pcm: make([]byte, BlockFrames*bytesPerFrame)}
	if e.clock == nil {
		e.clock = realClock{}
	}
	if e.open == nil {
		e.open = OpenPlayer
	}
	if e.cache == nil {
		e.cache = NewCache(nil, nil, 0)
	}
	if e.fixedLat == 0 {
		if ms, err := strconv.Atoi(os.Getenv("FS_AUDIO_LATENCY_MS")); err == nil && ms > 0 {
			e.fixedLat = time.Duration(ms) * time.Millisecond
		}
	}
	e.wg.Add(1)
	go e.prepLoop()
	return e
}

// Start runs the output loop in the background: the player starts now and streams
// silence until a voice plays.
func (e *Engine) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started || e.closed {
		return
	}
	e.started = true
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		for {
			select {
			case <-e.quit:
				return
			default:
			}
			_ = e.Step()
		}
	}()
}

// Now is the engine clock.
func (e *Engine) Now() time.Time { return e.clock.Now() }

// Cache is the engine's PCM cache.
func (e *Engine) Cache() *Cache { return e.cache }

// Close stops the loop, closes the player and the cache, and waits for all of it.
func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	out := e.out
	e.mu.Unlock()
	close(e.quit)
	if out != nil {
		_ = out.Close() // unblocks a pending Write
	}
	e.wg.Wait()
	e.mu.Lock()
	if e.out != nil && e.out != out {
		_ = e.out.Close()
	}
	e.out = nil
	e.mu.Unlock()
	e.cache.Close()
}

// Latency is the current output latency estimate.
func (e *Engine) Latency() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.latencyLocked()
}

func (e *Engine) latencyLocked() time.Duration {
	if e.fixedLat > 0 {
		return e.fixedLat
	}
	if e.out != nil {
		return e.out.Latency()
	}
	return 0
}

// output returns the output to write to, (re)opening the player when needed.
func (e *Engine) output() (Output, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, errClosed
	}
	if e.out != nil && (!e.isNull || errors.Is(e.outErr, ErrNoPlayer) || e.clock.Now().Before(e.retryAt)) {
		out := e.out
		e.mu.Unlock()
		return out, nil
	}
	e.mu.Unlock()

	out, err := e.open()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		if out != nil {
			_ = out.Close()
		}
		return nil, errClosed
	}
	if err != nil {
		e.outErr = err
		e.retryAt = e.clock.Now().Add(retryOutput)
		if e.out == nil {
			e.out, e.isNull = newNullOutput(), true
		}
		return e.out, nil
	}
	if e.out != nil {
		_ = e.out.Close() // the null output
	}
	e.out, e.isNull, e.outErr, e.openedAt = out, false, nil, e.clock.Now()
	return out, nil
}

// failed drops an output whose Write failed (the player quit).
func (e *Engine) failed(out Output) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.out != out || e.closed {
		return
	}
	_ = out.Close()
	e.out = nil
	if e.clock.Now().Sub(e.openedAt) < time.Second { // crashes right away: back off
		e.out, e.isNull = newNullOutput(), true
		e.outErr = errors.New("Wiedergabe ohne Ton: Der Audioplayer beendet sich sofort. Audioausgabe des Systems prüfen.")
		e.retryAt = e.clock.Now().Add(retryOutput)
	}
}

var errClosed = errors.New("audio engine closed")

// Step renders one block for all voices and writes it to the output.
func (e *Engine) Step() error {
	out, err := e.output()
	if err != nil {
		return err
	}
	e.mu.Lock()
	voices := append([]*Voice(nil), e.voices...)
	lat := e.latencyLocked()
	var outErr error
	if e.isNull {
		outErr = e.outErr
	}
	e.mu.Unlock()

	now := e.clock.Now()
	heard := now.Add(lat)
	clear(e.mix)
	for _, v := range voices {
		if v.render(heard, BlockFrames, e.mix) && outErr != nil {
			v.report(outErr)
		}
	}
	for i, x := range e.mix {
		s := int16(math.Round(float64(SoftClip(x)) * 32767))
		e.pcm[2*i] = byte(s)
		e.pcm[2*i+1] = byte(uint16(s) >> 8)
	}
	if e.onWrite != nil {
		e.onWrite(now, e.pcm)
	}
	if _, err := out.Write(e.pcm); err != nil {
		e.failed(out)
		return err
	}
	return nil
}

// AddVoice adds a mixer input (one per session). onError receives problems that keep
// it silent (missing ffmpeg or player), at most once, from the engine goroutine.
func (e *Engine) AddVoice(onError func(error)) *Voice {
	v := &Voice{e: e, onError: onError}
	e.mu.Lock()
	e.voices = append(e.voices, v)
	e.mu.Unlock()
	return v
}

// RemoveVoice takes v out of the mix and releases its audio: decodes no other voice
// needs are stopped and their PCM is freed.
func (e *Engine) RemoveVoice(v *Voice) {
	e.mu.Lock()
	found := false
	for i, w := range e.voices {
		if w == v {
			e.voices = append(e.voices[:i], e.voices[i+1:]...)
			found = true
			break
		}
	}
	closed := e.closed
	e.mu.Unlock()
	v.pmu.Lock()
	v.removed = true
	v.pmu.Unlock()
	if !found || closed {
		return
	}
	select {
	case e.removed <- v:
	case <-e.quit:
	}
}

// Voices is the number of voices.
func (e *Engine) Voices() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.voices)
}

// prepLoop turns project snapshots into plans, which starts the decodes they need.
func (e *Engine) prepLoop() {
	defer e.wg.Done()
	for {
		select {
		case <-e.quit:
			return
		case v := <-e.removed:
			e.release(v)
		case v := <-e.prep:
			v.pmu.Lock()
			p, removed := v.pending, v.removed
			v.pending, v.queued = nil, false
			v.pmu.Unlock()
			if removed {
				e.release(v)
				continue
			}
			if p == nil {
				continue
			}
			plan := BuildPlan(p, e.lookup)
			e.relinked(v, plan.keys)
			v.next.Store(plan)
		}
	}
}

func (e *Engine) lookup(m model.MediaItem) (Source, error) {
	pcm, err := e.cache.Get(m.Path)
	if err != nil {
		return nil, err // offline: plays silence like export leaves it out
	}
	if err := pcm.Err(); err != nil && !errors.Is(err, ErrNoFFmpeg) {
		return nil, err // decoded with an error (no audio stream)
	}
	return pcm, nil
}

// relinked records the keys v's new plan uses (pinning them in the cache) and drops
// cached audio that a media ID no longer maps to (relink, replaced file) unless another
// voice still plays it. Media that merely left the timeline (clip deleted, cut) keeps its
// PCM, unpinned, so Undo or Paste plays at once; the cache's LRU frees it when needed.
func (e *Engine) relinked(v *Voice, keys map[string]string) {
	old := e.keys[v]
	e.keys[v] = keys
	e.cache.pin(keys)
	e.cache.unpin(old)
	for id, k := range old {
		if now := keys[id]; now == "" || now == k {
			continue
		}
		if !e.keyUsed(k) {
			e.cache.Drop(k)
		}
	}
}

// release forgets a removed voice: its keys are unpinned and every key no other voice
// uses is dropped, which stops its decode and frees its PCM. Prep goroutine only.
func (e *Engine) release(v *Voice) {
	old, ok := e.keys[v]
	if !ok {
		return
	}
	delete(e.keys, v)
	e.cache.unpin(old)
	for _, k := range old {
		if !e.keyUsed(k) {
			e.cache.Drop(k)
		}
	}
}

// keyUsed reports whether any voice's current plan uses key k. Prep goroutine only.
func (e *Engine) keyUsed(k string) bool {
	for _, other := range e.keys {
		for _, ok := range other {
			if ok == k {
				return true
			}
		}
	}
	return false
}

// Transport is a session's play state, published by the UI thread.
type Transport struct {
	Playing bool
	Held    bool    // another component makes the sound (audio page preview)
	Rate    float64 // session playback rate
	Loop    bool
	FPS     int
	// Frame is the playhead at time At. Exact is true when it was exactly on the frame
	// (playback just started); otherwise the true position lies up to a frame ahead.
	Frame int
	At    time.Time
	Exact bool
	// Gen changes whenever playback (re)starts or changes speed, forcing a resync.
	Gen int
	// Scrub changes when the paused playhead moved (drag, step): play a short snippet there.
	Scrub int
	// Start and End bound playback (In/Out or the timeline), as in session.Advance.
	Start, End int
}

// posAt extrapolates the program position (in frames) at time t, wrapping in loop
// mode. ok is false past the end of playback.
func (t Transport) posAt(at time.Time) (float64, bool) {
	x := float64(t.Frame) + at.Sub(t.At).Seconds()*float64(t.FPS)*t.Rate
	s, e := float64(t.Start), float64(t.End)
	switch {
	case t.Loop && e > s && t.Rate > 0 && x >= e:
		x = s + math.Mod(x-e, e-s)
	case t.Loop && e > s && t.Rate < 0 && x < s:
		x = e - math.Mod(s-x, e-s)
	case t.Rate > 0 && x >= e, t.Rate < 0 && x < s:
		return 0, false
	}
	return x, true
}

// audibleRate reports whether the engine plays sound at rate r: normal speed, reverse
// and double speed (without pitch correction). Faster shuttle speeds stay silent.
func audibleRate(r float64) bool { return r == 1 || r == -1 || r == 2 || r == -2 }

// Voice is one session's input to the mix.
type Voice struct {
	e        *Engine
	onError  func(error)
	reported atomic.Bool

	tmu sync.Mutex
	tr  Transport

	pmu     sync.Mutex
	pending *model.Project
	queued  bool
	removed bool
	next    atomic.Pointer[Plan]

	// mixer state, engine goroutine only
	plan   *Plan
	active bool
	pos    int64
	step   int
	gen    int
	win    Window
	buf    []float32
	old    []float32
	trk    [][]float32
	trkOld [][]float32

	scrub     int
	scrubLeft int
	checked   *Plan // plan whose errors were looked at

	lmu      sync.Mutex
	lastPlan *Plan
	ring     [ringSize]blockLevel
	ringN    int
	ids      []string
	jumps    int
	blocks   int
}

// SetTransport publishes the session's play state. Cheap; call on every change.
func (v *Voice) SetTransport(t Transport) {
	v.tmu.Lock()
	v.tr = t
	v.tmu.Unlock()
}

func (v *Voice) transport() Transport {
	v.tmu.Lock()
	defer v.tmu.Unlock()
	return v.tr
}

// SetProject hands the voice a snapshot of the project (the voice owns it from now
// on). The mix switches to it within a block of the plan being ready.
func (v *Voice) SetProject(p *model.Project) {
	v.pmu.Lock()
	defer v.pmu.Unlock()
	if v.removed {
		return
	}
	v.pending = p
	if !v.queued {
		select {
		case v.e.prep <- v:
			v.queued = true
		default: // the prep loop is behind; the next SetProject retries
		}
	}
}

// Plan returns the newest prepared plan (nil before the first one is ready).
func (v *Voice) Plan() *Plan {
	if p := v.next.Load(); p != nil {
		return p
	}
	v.lmu.Lock()
	defer v.lmu.Unlock()
	return v.lastPlan
}

func (v *Voice) report(err error) {
	if err != nil && v.onError != nil && v.reported.CompareAndSwap(false, true) {
		v.onError(err)
	}
}

// Stats returns how many blocks the voice sounded in and how often it resynced.
func (v *Voice) Stats() (blocks, jumps int) {
	v.lmu.Lock()
	defer v.lmu.Unlock()
	return v.blocks, v.jumps
}

// render adds the voice's next block, heard at time heard, to mix. It reports whether
// the voice is sounding.
func (v *Voice) render(heard time.Time, n int, mix []float32) bool {
	tr := v.transport()
	plan := v.plan
	if np := v.next.Swap(nil); np != nil {
		plan = np
	}
	active, jump := false, false
	var pos int64
	var step int
	var win Window
	if plan != nil && tr.Playing && !tr.Held && tr.FPS > 0 && audibleRate(tr.Rate) {
		if x, ok := tr.posAt(heard); ok {
			active, step = true, int(tr.Rate)
			target := int64(math.Round(x * SampleRate / float64(tr.FPS)))
			frame := float64(SampleRate) / float64(tr.FPS)
			tol := resyncTolerance.Seconds() * SampleRate * math.Abs(tr.Rate)
			lo, hi := -tol, frame+tol // the playhead lags the true position by < 1 frame
			if step < 0 {
				lo, hi = -frame-tol, tol
			}
			d := float64(v.pos - target)
			if !v.active || step != v.step || tr.Gen != v.gen || d < lo || d > hi {
				jump, pos = true, target
				if !tr.Exact {
					pos += int64(float64(step/abs(step)) * frame / 2)
				}
			} else {
				pos = v.pos
			}
			if step > 0 {
				win = Window{0, FramesToSamples(tr.End, tr.FPS)}
			} else {
				win = Window{FramesToSamples(tr.Start, tr.FPS), math.MaxInt64}
			}
		}
	}
	scrub := tr.Scrub != v.scrub
	v.scrub = tr.Scrub
	if plan != nil && !tr.Playing && !tr.Held && tr.FPS > 0 {
		if scrub {
			v.scrubLeft, jump = scrubBlocks, true
			pos = FramesToSamples(tr.Frame, tr.FPS)
		} else {
			pos = v.pos
		}
		if v.scrubLeft > 0 {
			v.scrubLeft--
			active, step = true, 1
		}
	}
	if tr.Playing || tr.Held {
		v.scrubLeft = 0
	}
	changed := jump || plan != v.plan || active != v.active
	if plan != v.plan {
		v.lmu.Lock()
		v.lastPlan = plan
		v.ids = v.ids[:0]
		if plan != nil {
			for _, t := range plan.Tracks {
				v.ids = append(v.ids, t.ID)
			}
		}
		v.lmu.Unlock()
	}
	if !active && !v.active {
		v.plan = plan
		return false
	}
	if active && (!v.active || v.checked != plan) && !v.reported.Load() {
		v.checked = plan
		v.report(plan.err())
	}

	v.buf, v.trk = scratch(v.buf, v.trk, plan, n)
	if active {
		plan.Render(pos, step, win, v.buf, v.trk, n)
	} else {
		clear(v.buf)
		for _, t := range v.trk {
			clear(t)
		}
	}
	if changed && v.active && v.plan != nil { // crossfade from where the old state would go on
		v.old, v.trkOld = scratch(v.old, v.trkOld, v.plan, n)
		v.plan.Render(v.pos, v.step, v.win, v.old, v.trkOld, n)
		for i := range n {
			w := (float32(i) + 0.5) / float32(n)
			v.buf[2*i] = v.buf[2*i]*w + v.old[2*i]*(1-w)
			v.buf[2*i+1] = v.buf[2*i+1]*w + v.old[2*i+1]*(1-w)
			for ti := range v.trk {
				o := oldTrack(v.plan, plan, ti, v.trkOld)
				for c := range 2 {
					x := v.trk[ti][2*i+c] * w
					if o != nil {
						x += o[2*i+c] * (1 - w)
					}
					v.trk[ti][2*i+c] = x
				}
			}
		}
	} else if changed && !v.active { // fade in from silence
		for i := range n {
			w := (float32(i) + 0.5) / float32(n)
			v.buf[2*i] *= w
			v.buf[2*i+1] *= w
			for ti := range v.trk {
				v.trk[ti][2*i] *= w
				v.trk[ti][2*i+1] *= w
			}
		}
	}
	for i, x := range v.buf[:2*n] {
		mix[i] += x
	}
	v.record(heard, n, plan)

	if active {
		v.pos = pos + int64(step)*int64(n)
	}
	v.plan, v.active, v.step, v.gen, v.win = plan, active, step, tr.Gen, win
	v.lmu.Lock()
	v.blocks++
	if jump {
		v.jumps++
	}
	v.lmu.Unlock()
	return true
}

// err returns the reason the plan cannot sound, if any.
func (pl *Plan) err() error {
	if pl.Err != nil {
		return pl.Err
	}
	for _, t := range pl.Tracks {
		for _, c := range t.Clips {
			if pcm, ok := c.Source.(*PCM); ok && errors.Is(pcm.Err(), ErrNoFFmpeg) {
				return ErrNoFFmpeg
			}
		}
	}
	return nil
}

// oldTrack finds track ti of plan in the previous plan's buffers (same ID).
func oldTrack(prev, plan *Plan, ti int, bufs [][]float32) []float32 {
	if plan == nil || ti >= len(plan.Tracks) {
		return nil
	}
	id := plan.Tracks[ti].ID
	if ti < len(prev.Tracks) && prev.Tracks[ti].ID == id {
		return bufs[ti]
	}
	for i, t := range prev.Tracks {
		if t.ID == id {
			return bufs[i]
		}
	}
	return nil
}

// scratch sizes the mix and per-track buffers for plan.
func scratch(buf []float32, trk [][]float32, plan *Plan, n int) ([]float32, [][]float32) {
	if len(buf) < 2*n {
		buf = make([]float32, 2*n)
	}
	tracks := 0
	if plan != nil {
		tracks = len(plan.Tracks)
	}
	for len(trk) < tracks {
		trk = append(trk, make([]float32, 2*n))
	}
	for i := range trk {
		if len(trk[i]) < 2*n {
			trk[i] = make([]float32, 2*n)
		}
	}
	return buf, trk[:tracks]
}
