package preview

import (
	"context"
	"image"
	"sync"
	"sync/atomic"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
)

// ReqKind says why a frame is wanted.
type ReqKind int

const (
	// ReqPlay is a frame of running playback: latest wins, in-flight renders are never
	// cancelled by newer play requests (they would starve when a frame takes longer than
	// a period), and the quality follows the controller.
	ReqPlay ReqKind = iota
	// ReqScrub is a seek while paused or a drag; it cancels whatever is in flight and
	// renders at reduced quality while requests keep coming.
	ReqScrub
	// ReqStill is a full-quality picture of a position at rest.
	ReqStill
)

// Renderer is what the scheduler needs from the compositor; *render.Renderer fits.
type Renderer interface {
	RenderInto(ctx context.Context, dst *image.RGBA, p *model.Project, frame int, o render.Options) (render.Info, error)
}

// Config tunes a Scheduler. The zero value is usable.
type Config struct {
	Now         func() time.Time // virtual time in tests
	RefineDelay time.Duration    // idle time before the full-quality still (default 150 ms)
	ScrubGap    time.Duration    // seeks closer than this count as dragging (default 120 ms)
	MaxH        int              // cap on the render height while playing at full quality; 0 = none
	Cache       *FrameCache      // nil = no cache
	Stats       *Stats           // nil = private
	Governor    *LoadGovernor    // nil = never busy
	// PostProcess runs on the scheduler goroutine for every frame before it is
	// published (also cache hits); the viewer uses it to prepare the scopes picture.
	PostProcess func(*Frame)
	// Warm starts the decoders for frames from pos at w x h, ahead of a quality step up.
	Warm func(p *model.Project, pos, w, h int)
}

type job struct {
	pos   int
	kind  ReqKind
	at    time.Time
	proj  *model.Project
	rev   uint64
	tgt   Target
	pin   string
	skip  bool
	speed float64
	// isolated scrub request (not part of a drag): rendered at full quality
	isolated bool
}

// Scheduler renders the preview on one goroutine. Requests are coalesced to the newest;
// the result is published to a single ready slot the UI thread empties. It never
// touches the UI thread and never queues more than one frame.
type Scheduler struct {
	lastPlayW, lastPlayH int // size and time of the previous playback frame (scheduler goroutine)
	lastPlayAt           time.Time
	obsJob               *job // the job being observed by the quality controller (guarded by mu)
	cfg                  Config
	r                    Renderer
	st                   *Stats
	qc                   *QualityController

	mu     sync.Mutex
	proj   *model.Project
	rev    uint64
	tgt    Target
	pin    string
	skip   bool
	speed  float64
	period time.Duration
	pend   *job
	cancel context.CancelFunc

	lastScrub time.Time
	refine    bool
	refineGen uint64
	armedGen  uint64
	last      *job // last job rendered below the still quality
	timer     *time.Timer

	hasher    *Hasher
	hasherRev uint64
	hasherSet bool
	pool      *BufPool

	ready   atomic.Pointer[Frame]
	onReady atomic.Pointer[func()]
	seq     atomic.Uint64
	wake    chan struct{}
	quit    chan struct{}
	done    chan struct{}
	closed  atomic.Bool
}

// New starts a scheduler. Close it when done.
func New(cfg Config, r Renderer) *Scheduler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.RefineDelay <= 0 {
		cfg.RefineDelay = 150 * time.Millisecond
	}
	if cfg.ScrubGap <= 0 {
		cfg.ScrubGap = 120 * time.Millisecond
	}
	if cfg.Stats == nil {
		cfg.Stats = &Stats{}
	}
	s := &Scheduler{cfg: cfg, r: r, st: cfg.Stats, qc: NewQualityController(), pin: "auto", speed: 1,
		period: time.Second / 30,
		wake:   make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{})}
	if cfg.Warm != nil {
		s.qc.SetWarm(func(to Quality) { // called with s.mu held, from process
			if j := s.obsJob; j != nil {
				w, h := planSize(j, to, s.cfg.MaxH)
				cfg.Warm(j.proj, j.pos+int(qcWarm/s.period)+1, w, h)
			}
		})
	}
	go s.run()
	return s
}

// Done is closed when the scheduler is closed.
func (s *Scheduler) Done() <-chan struct{} { return s.quit }

// Stats returns the statistics collector.
func (s *Scheduler) Stats() *Stats { return s.st }

// Quality returns the automatic ladder step (ignoring pins).
func (s *Scheduler) Quality() Quality {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.qc.Level()
}

// QualityChanges counts automatic level changes (flapping checks).
func (s *Scheduler) QualityChanges() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.qc.Changes()
}

// SetProject installs the project snapshot to render (nobody may mutate it) and its
// revision counter. A new revision cancels the render in flight.
func (s *Scheduler) SetProject(p *model.Project, rev uint64) {
	s.mu.Lock()
	changed := s.proj != p || s.rev != rev
	s.proj, s.rev = p, rev
	if p != nil {
		if r := p.Rate(); r.Num > 0 && r.Den > 0 {
			s.period = time.Duration(int64(time.Second) * int64(r.Den) / int64(r.Num))
			s.st.SetProjectFPS(float64(r.Num) / float64(r.Den))
		}
	}
	if changed && s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}

// SetTarget sets the pixel size of the picture area. A new size forgets the buffer
// pool and re-renders.
func (s *Scheduler) SetTarget(t Target) {
	s.mu.Lock()
	changed := s.tgt != t
	s.tgt = t
	s.st.targetW.Store(int32(t.W))
	s.st.targetH.Store(int32(t.H))
	s.mu.Unlock()
	if changed {
		s.signal()
	}
}

// SetQualityPin selects auto, full, half or quarter.
func (s *Scheduler) SetQualityPin(mode string) {
	switch mode {
	case "full", "half", "quarter":
	default:
		mode = "auto"
	}
	s.mu.Lock()
	s.pin = mode
	s.st.pinned.Store(mode != "auto")
	s.mu.Unlock()
}

// SetCompare renders without grading (before/after).
func (s *Scheduler) SetCompare(skipGrade bool) {
	s.mu.Lock()
	s.skip = skipGrade
	s.mu.Unlock()
}

// SetSpeed sets the playback rate passed on to the decoders (1 = normal, 0 = paused).
func (s *Scheduler) SetSpeed(v float64) {
	s.mu.Lock()
	s.speed = v
	s.mu.Unlock()
}

// SetMaxH caps the render height of full-quality playback frames (0 = no cap).
func (s *Scheduler) SetMaxH(h int) {
	s.mu.Lock()
	s.cfg.MaxH = h
	s.mu.Unlock()
}

// OnReady registers the notifier called (from the scheduler goroutine) whenever a new
// frame sits in the ready slot. The viewer coalesces it into one UI post.
func (s *Scheduler) OnReady(fn func()) {
	if fn == nil {
		s.onReady.Store(nil)
		return
	}
	s.onReady.Store(&fn)
}

// Ready is the single ready slot: the newest rendered frame the UI has not taken.
func (s *Scheduler) Ready() *atomic.Pointer[Frame] { return &s.ready }

// Take empties the ready slot and returns the frame (the caller owns its reference).
func (s *Scheduler) Take() *Frame { return s.ready.Swap(nil) }

// Request asks for the picture at timeline frame pos. It never blocks.
func (s *Scheduler) Request(pos int, kind ReqKind) {
	now := s.cfg.Now()
	s.mu.Lock()
	j := &job{pos: pos, kind: kind, at: now, proj: s.proj, rev: s.rev, tgt: s.tgt,
		pin: s.pin, skip: s.skip, speed: s.speed}
	if kind == ReqScrub {
		j.isolated = s.lastScrub.IsZero() || now.Sub(s.lastScrub) > s.cfg.ScrubGap
		s.lastScrub = now
	}
	if s.pend != nil && s.pend.kind == ReqPlay && kind == ReqPlay {
		s.st.Dropped(1) // a play frame superseded before it started
	}
	s.pend = j
	s.refine = false
	s.refineGen++
	if kind != ReqPlay && s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.signal()
}

// Close stops the goroutine and frees the ready frame. It is safe to call twice.
func (s *Scheduler) Close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	close(s.quit)
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
	<-s.done
	s.ready.Swap(nil).Release()
}

func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) run() {
	defer close(s.done)
	for {
		select {
		case <-s.quit:
			return
		case <-s.wake:
		}
		for {
			j := s.take()
			if j == nil {
				break
			}
			s.process(j)
			select {
			case <-s.quit:
				return
			default:
			}
		}
	}
}

func (s *Scheduler) take() *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.pend; j != nil {
		s.pend = nil
		return j
	}
	if s.refine && s.last != nil {
		s.refine = false
		j := *s.last
		j.kind, j.at = ReqStill, s.cfg.Now()
		j.proj, j.rev, j.tgt, j.pin, j.skip = s.proj, s.rev, s.tgt, s.pin, s.skip
		return &j
	}
	return nil
}

// qualityFor picks the ladder step for a job.
func (s *Scheduler) qualityFor(j *job) Quality {
	switch j.pin {
	case "full":
		return QFull
	case "half":
		return QHalf
	case "quarter":
		return QQuarter
	}
	s.mu.Lock()
	lvl := s.qc.Level()
	s.mu.Unlock()
	switch j.kind {
	case ReqStill:
		return QFull
	case ReqScrub:
		if j.isolated {
			return QFull
		}
		return max(lvl, QHalf)
	}
	if j.speed > 2 || j.speed < -2 {
		lvl = max(lvl, QHalf)
	}
	return lvl
}

// planSize is the render size of a job at quality q.
func planSize(j *job, q Quality, maxH int) (int, int) {
	tgt := j.tgt
	if j.kind == ReqPlay && maxH > 0 && tgt.H > maxH {
		tgt = Target{W: max(1, tgt.W*maxH/tgt.H), H: maxH}
	}
	return SizeFor(tgt, q)
}

// PlanSize returns the size the next frame of the given kind would be rendered at.
func (s *Scheduler) PlanSize(kind ReqKind, speed float64) (int, int) {
	s.mu.Lock()
	j := &job{kind: kind, tgt: s.tgt, pin: s.pin, speed: speed}
	maxH := s.cfg.MaxH
	s.mu.Unlock()
	return planSize(j, s.qualityFor(j), maxH)
}

func (s *Scheduler) hasherFor(j *job) *Hasher {
	if !s.hasherSet || s.hasherRev != j.rev || s.hasher.proj != j.proj {
		s.hasher, s.hasherRev, s.hasherSet = BuildHasher(j.proj), j.rev, true
	}
	return s.hasher
}

func (s *Scheduler) process(j *job) {
	if j.proj == nil || j.tgt.Empty() {
		return
	}
	s.mu.Lock()
	half := s.qc.HalfRate()
	maxH := s.cfg.MaxH
	period := s.period
	s.mu.Unlock()
	if j.kind == ReqPlay && half && j.pos%2 != 0 {
		return // last-resort frame-rate halving: the picture holds
	}

	if j.kind == ReqPlay && j.pin == "auto" {
		s.mu.Lock()
		s.qc.Prime(StartLevel(j.proj, j.pos))
		s.mu.Unlock()
	}
	q := s.qualityFor(j)
	w, h := planSize(j, q, maxH)
	if w == 0 {
		return
	}

	hs := s.hasherFor(j)
	key := CacheKey{hs.FrameHash(j.pos, j.skip), j.pos, uint16(w), uint16(h), q}
	cache := s.cfg.Cache
	if cache != nil {
		cache.Pin(j.pos-int(time.Second/period), j.pos+int(time.Second/period))
		if f, ok := cache.Get(key); ok {
			s.st.CacheLookup(true)
			s.publish(f, j, period, true)
			s.scheduleRefine(j, q, w, h, false)
			return
		}
		s.st.CacheLookup(false)
		if j.kind == ReqScrub {
			// show a close cached picture at once while the exact one renders
			hashAt := func(fr int) uint64 { return hs.FrameHash(fr, j.skip) }
			if f, _, ok := cache.GetNearest(hashAt, j.pos, 6, w, h, q); ok {
				s.publish(f, j, period, true)
			}
		}
	}

	if s.pool == nil || func() bool { pw, ph := s.pool.Size(); return pw != w || ph != h }() {
		s.pool = NewBufPool(w, h, 6)
	}
	f := s.pool.Get()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	stale := s.closed.Load() || (j.kind != ReqPlay && s.pend != nil)
	s.mu.Unlock()
	if stale {
		cancel()
	}
	t0 := s.cfg.Now()
	opts := render.Options{SkipGrade: j.skip}
	if j.kind == ReqPlay {
		opts.Rate = j.speed
	}
	rep := &render.Report{}
	ph := &render.PreviewHint{Scrub: j.kind == ReqScrub && !j.isolated, Accurate: j.kind == ReqStill,
		Q: media.Quality(q), RenderH: h, Report: rep}
	if j.kind == ReqScrub && !j.isolated {
		ph.Deadline = s.cfg.Now().Add(period)
	}
	opts.Preview = ph
	_, err := s.r.RenderInto(ctx, f.Img, j.proj, j.pos, opts)
	cancel()
	s.mu.Lock()
	s.cancel = nil
	s.mu.Unlock()
	if err != nil {
		f.Release()
		return
	}
	cost := s.cfg.Now().Sub(t0)
	f.Pos, f.Gen, f.Q, f.RenderNS = j.pos, j.rev, q, int64(cost)
	f.Src = Source(rep.Src)
	s.st.SetKFOnly(rep.KFOnly)
	late := j.kind == ReqPlay && s.cfg.Now().Sub(j.at) > period
	cold := false
	if j.kind == ReqPlay {
		now := s.cfg.Now()
		cold = s.lastPlayW != w || s.lastPlayH != h || t0.Sub(s.lastPlayAt) > 10*period
		s.lastPlayW, s.lastPlayH, s.lastPlayAt = w, h, now
	}
	f.Late = late
	if cache != nil && !rep.Approx {
		cache.Put(key, f)
	}
	s.st.addRender(int64(cost))
	if j.kind == ReqPlay {
		busy := s.cfg.Governor != nil && s.cfg.Governor.Busy()
		s.mu.Lock()
		s.obsJob = j
		s.qc.Observe(Sample{Cold: cold, At: s.cfg.Now(), Period: period, RenderNS: int64(cost), Late: late,
			UIBusy: busy, Playing: true, Speed: j.speed})
		s.st.underrun.Store(s.qc.UnderRun())
		s.mu.Unlock()
	}
	s.publish(f, j, period, false)
	s.scheduleRefine(j, q, w, h, rep.Approx)
}

// publish puts f into the ready slot (it takes over the caller's reference), replacing
// and releasing an older unshown frame.
func (s *Scheduler) publish(f *Frame, j *job, period time.Duration, hit bool) {
	if s.cfg.PostProcess != nil {
		s.cfg.PostProcess(f)
	}
	st := s.st
	st.rendered.Add(1)
	if f.Late {
		st.late.Add(1)
	}
	st.quality.Store(int32(f.Q))
	st.renderW.Store(int32(f.Img.Rect.Dx()))
	st.renderH.Store(int32(f.Img.Rect.Dy()))
	src := f.Src
	if hit {
		src = SrcCache
	}
	st.src.Store(int32(src))
	if old := s.ready.Swap(f); old != nil {
		old.Release()
		if j.kind == ReqPlay {
			st.Dropped(1)
		}
	}
	if fn := s.onReady.Load(); fn != nil {
		(*fn)()
	}
}

// scheduleRefine arms the idle timer that re-renders the position at full quality
// when the picture shown is smaller than what the pins and the widget allow.
func (s *Scheduler) scheduleRefine(j *job, q Quality, w, h int, approx bool) {
	if j.kind == ReqStill {
		return
	}
	want := Quality(QFull)
	switch j.pin {
	case "half":
		want = QHalf
	case "quarter":
		want = QQuarter
	}
	fw, fh := SizeFor(j.tgt, want)
	if q == want && w == fw && h == fh && !approx {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pend != nil {
		return
	}
	s.last = j
	s.armedGen = s.refineGen
	if s.timer == nil {
		s.timer = time.AfterFunc(s.cfg.RefineDelay, s.refineFired)
	} else {
		s.timer.Reset(s.cfg.RefineDelay)
	}
}

func (s *Scheduler) refineFired() {
	s.mu.Lock()
	if s.refineGen == s.armedGen && s.pend == nil && !s.closed.Load() {
		s.refine = true
	}
	s.mu.Unlock()
	s.signal()
}
