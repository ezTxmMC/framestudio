package color

import (
	"image"
	"sync"
	"time"

	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
)

// Size limit and rate of the frames a frameSource renders for scopes. Scopes subsample
// anyway; a small frame keeps the extra render cheap next to the program viewer.
const (
	sourceW, sourceH = 480, 270
	sourceInterval   = 100 * time.Millisecond // ~10 frames a second while playing
)

// frameSource renders the session's current playhead frame on its own goroutine for the
// detached scopes panel, independent of any viewer: on every seek, edit and playback
// tick it renders a small frame from a project clone and hands it to deliver on the UI
// thread. Only the newest request is kept (latest wins) and playback is throttled to
// sourceInterval.
type frameSource struct {
	s       *session.Session
	r       *render.Renderer
	deliver func(img *image.NRGBA) // UI thread
	post    func(func())           // to the UI thread; tests may replace it
	unsub   func()
	snap    *model.Project
	last    frameReq
	closed  bool

	mu      sync.Mutex
	pending *frameReq
	ready   *image.NRGBA
	running bool
	wake    chan struct{}
	quit    chan struct{}
	done    chan struct{}
}

type frameReq struct {
	p       *model.Project
	f       int
	w, h    int
	rate    float64
	playing bool
}

func newFrameSource(s *session.Session, r *render.Renderer, deliver func(*image.NRGBA)) *frameSource {
	fs := &frameSource{s: s, r: r, deliver: deliver, post: kit.Do,
		snap: s.Project.Clone(), wake: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{})}
	fs.unsub = s.Subscribe(fs.changed)
	return fs
}

func (fs *frameSource) changed(c session.Change) {
	if c&session.ChangeProject != 0 {
		fs.snap = fs.s.Project.Clone()
	}
	if c&(session.ChangeProject|session.ChangePlayhead|session.ChangePlayback) != 0 {
		fs.request()
	}
}

// frameSize fits the project frame into sourceW x sourceH.
func frameSize(p *model.Project) (int, int) {
	w, h := p.Settings.Width, p.Settings.Height
	if w <= 0 || h <= 0 {
		w, h = 1920, 1080
	}
	k := min(1, float64(sourceW)/float64(w), float64(sourceH)/float64(h))
	return max(1, int(float64(w)*k+0.5)), max(1, int(float64(h)*k+0.5))
}

// request asks for the current frame. UI thread.
func (fs *frameSource) request() {
	if fs.closed || fs.r == nil {
		return
	}
	w, h := frameSize(fs.snap)
	req := frameReq{p: fs.snap, f: fs.s.Playhead, w: w, h: h, playing: fs.s.Playing}
	if fs.s.Playing {
		req.rate = fs.s.Rate
	}
	if req == fs.last {
		return
	}
	fs.last = req
	fs.mu.Lock()
	fs.pending = &req
	if !fs.running {
		fs.running = true
		go fs.run()
	}
	fs.mu.Unlock()
	select {
	case fs.wake <- struct{}{}:
	default:
	}
}

func (fs *frameSource) run() {
	defer close(fs.done)
	var lastAt time.Time
	for {
		select {
		case <-fs.quit:
			return
		case <-fs.wake:
		}
		fs.mu.Lock()
		playing := fs.pending != nil && fs.pending.playing
		fs.mu.Unlock()
		if wait := sourceInterval - time.Since(lastAt); playing && wait > 0 {
			select { // throttle playback; newer requests replace the pending one meanwhile
			case <-fs.quit:
				return
			case <-time.After(wait):
			}
		}
		fs.mu.Lock()
		req := fs.pending
		fs.pending = nil
		fs.mu.Unlock()
		if req == nil {
			continue
		}
		lastAt = time.Now()
		img := fs.r.FrameWith(req.p, req.f, req.w, req.h, render.Options{Rate: req.rate})
		select {
		case <-fs.quit:
			return
		default:
		}
		fs.mu.Lock()
		queued := fs.ready != nil
		fs.ready = img
		fs.mu.Unlock()
		if !queued {
			fs.post(fs.showReady)
		}
	}
}

func (fs *frameSource) showReady() {
	fs.mu.Lock()
	img := fs.ready
	fs.ready = nil
	fs.mu.Unlock()
	if img != nil && !fs.closed {
		fs.deliver(img)
	}
}

// close unsubscribes and stops the render goroutine; a render in progress finishes on
// its own (it only reads a project clone) and its frame is dropped.
func (fs *frameSource) close() {
	if fs.closed {
		return
	}
	fs.closed = true
	fs.unsub()
	close(fs.quit)
}

// stopped reports whether the render goroutine has ended (or never started) within
// timeout.
func (fs *frameSource) stopped(timeout time.Duration) bool {
	fs.mu.Lock()
	running := fs.running
	fs.mu.Unlock()
	if !running {
		return true
	}
	select {
	case <-fs.done:
		return true
	case <-time.After(timeout):
		return false
	}
}
