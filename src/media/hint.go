package media

import (
	"context"
	"image"
	"time"

	"golang.org/x/image/draw"

	"framestudio-proto/src/model"
)

// Quality is the preview resolution step the viewer renders at (same numbering as the
// preview package: 0 full, 1 half, 2 quarter).
type Quality int

const (
	QFull Quality = iota
	QHalf
	QQuarter
)

// MediaHint tells VideoFrameInto what the picture is for.
type MediaHint struct {
	Speed    float64   // playback speed, negative = reverse, 0 = unknown
	Scrub    bool      // dragging the playhead: nearest picture within a frame period beats exactness
	RenderH  int       // height the compositor renders at (proxy choice); 0 = h
	Q        Quality   // preview quality step
	Accurate bool      // export or refine: the original, software, exactly
	Deadline time.Time // return an approximate picture when the exact one is later than this
}

// Decoded is a frame for the compositor. Pix is shared with the frame cache: read it,
// never write it, and call Release when done (the buffer is reused afterwards).
type Decoded struct {
	Pix     []byte
	W, H    int // can be smaller than asked for when Approx (keyframe ladder)
	Stride  int
	Src     SourceKind
	Approx  bool // a neighbour or keyframe picture stands in for the exact frame
	KFOnly  bool // keyframe-only shuttle (the viewer shows "Nur Keyframes")
	Release func()
}

func (d Decoded) release() {
	if d.Release != nil {
		d.Release()
	}
}

// approxNeighbours is how far from the wanted frame a cached frame may stand in.
const approxNeighbours = 6

// VideoFrameInto returns frame t of video m at the project rate scaled to fit w x h,
// ctx-cancellable, from pooled buffers, using a proxy and hardware decoding where the
// hint and the settings allow. For m.Kind image it returns the scaled still.
//
// With a Deadline it never waits past it when something close is at hand: a cached
// neighbour within a few frames or the keyframe ladder picture, flagged Approx, while
// the exact frame keeps decoding into the cache.
func (c *Cache) VideoFrameInto(ctx context.Context, m model.MediaItem, t time.Duration, rate model.Rate, w, h int, hint MediaHint) (Decoded, error) {
	fps := rate.Or(model.Rate25).Reduced()
	n := max(fps.DurationToFrames(t), 0)
	o := frameOpts{proxy: c.proxyFor(m, hint, h), ladder: true}
	if m.Kind == model.KindVideo && (hint.Scrub || !hint.Deadline.IsZero() || hint.Speed >= 4 || hint.Speed <= -4) {
		c.ensureLadder(m)
	}
	if hint.Deadline.IsZero() || m.Kind != model.KindVideo {
		res, err := c.frameFB(ctx, m, n, fps, w, h, hint.Speed, o)
		if err != nil {
			return Decoded{}, err
		}
		return toDecoded(res), nil
	}

	type outcome struct {
		res frameRes
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		res, err := c.frameFB(ctx, m, n, fps, w, h, hint.Speed, o)
		ch <- outcome{res, err}
	}()
	timer := time.NewTimer(time.Until(hint.Deadline))
	defer timer.Stop()
	select {
	case out := <-ch:
		if out.err != nil {
			return Decoded{}, out.err
		}
		return toDecoded(out.res), nil
	case <-ctx.Done():
		go func() {
			if out := <-ch; out.err == nil {
				out.res.done()
			}
		}()
		return Decoded{}, ctx.Err()
	case <-timer.C:
	}
	if d, ok := c.approx(m, n, fps, w, h, o.proxy); ok {
		go func() { // the exact frame lands in the cache; drop the reference
			if out := <-ch; out.err == nil {
				out.res.done()
			}
		}()
		return d, nil
	}
	select {
	case out := <-ch:
		if out.err != nil {
			return Decoded{}, out.err
		}
		return toDecoded(out.res), nil
	case <-ctx.Done():
		go func() {
			if out := <-ch; out.err == nil {
				out.res.done()
			}
		}()
		return Decoded{}, ctx.Err()
	}
}

// frameFB is frameX that falls back to the original when the proxy cannot be decoded
// (missing or corrupt file); the proxy is then left alone for a minute.
func (c *Cache) frameFB(ctx context.Context, m model.MediaItem, n int, fps model.Rate, w, h int, speed float64, o frameOpts) (frameRes, error) {
	res, err := c.frameX(ctx, m, n, fps, w, h, speed, o)
	if err != nil && o.proxy != "" && ctx.Err() == nil {
		c.badProxy.Store(o.proxy, time.Now())
		o.proxy = ""
		return c.frameX(ctx, m, n, fps, w, h, speed, o)
	}
	return res, err
}

// approx finds a stand-in for frame n at once: a cached neighbour, else the keyframe
// ladder picture.
func (c *Cache) approx(m model.MediaItem, n int, fps model.Rate, w, h int, proxyPath string) (Decoded, bool) {
	w, h = fitSize(m.Width, m.Height, w, h)
	s := stream{id: m.ID, path: m.Path, fps: fps, w: w, h: h, proxy: proxyPath}
	for d := 1; d <= approxNeighbours; d++ {
		for _, k := range []int{n - d, n + d} {
			if k < 0 {
				continue
			}
			if r, ok := c.cached(s, k); ok {
				r.approx = true
				return toDecoded(r), true
			}
		}
	}
	if img := c.ladderFor(m).at(fps.FramesToDuration(n)); img != nil {
		b := img.Bounds()
		return Decoded{Pix: img.Pix, W: b.Dx(), H: b.Dy(), Stride: img.Stride, Src: SrcCache, Approx: true, Release: func() {}}, true
	}
	return Decoded{}, false
}

func toDecoded(r frameRes) Decoded {
	rel := r.release
	if rel == nil {
		rel = func() {}
	}
	b := r.img.Bounds()
	if rgba, ok := r.img.(*image.RGBA); ok && rgba.Rect.Min == (image.Point{}) {
		return Decoded{Pix: rgba.Pix, W: b.Dx(), H: b.Dy(), Stride: rgba.Stride, Src: r.src,
			Approx: r.approx, KFOnly: r.kfOnly, Release: rel}
	}
	// Anything else (a still in another colour model) is converted once.
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), r.img, b.Min, draw.Src)
	rel()
	return Decoded{Pix: dst.Pix, W: b.Dx(), H: b.Dy(), Stride: dst.Stride, Src: r.src,
		Approx: r.approx, KFOnly: r.kfOnly, Release: func() {}}
}

// Prewarm starts what playback from t will need, without waiting: the decoder at the
// position, the keyframe index and the ladder. Call it on every seek end and when a
// project or clip is opened.
func (c *Cache) Prewarm(m model.MediaItem, t time.Duration, rate model.Rate, w, h int) {
	if m.Kind != model.KindVideo {
		return
	}
	fps := rate.Or(model.Rate25).Reduced()
	n := max(fps.DurationToFrames(t), 0)
	go func() {
		c.ensureLadder(m)
		_, _ = c.sourceInfo(m)
		sw, sh := fitSize(m.Width, m.Height, w, h)
		if sw <= 0 || sh <= 0 {
			return
		}
		n := clampFrame(m, n, fps)
		s := stream{id: m.ID, path: m.Path, fps: fps, w: sw, h: sh, proxy: c.proxyFor(m, MediaHint{RenderH: h}, h)}
		if _, ok := c.frames.peek(s.frameKey(n)); ok {
			return
		}
		_, _ = c.prefetcherFor(m, s, n)
	}()
}

// Budget sets the byte budget of the decoded-frame cache and of the idle buffer pool.
func (c *Cache) Budget(bytes int64) {
	if bytes <= 0 {
		bytes = frameBudget
	}
	c.frames.setBudget(bytes)
	c.pool.SetLimit(bytes / 4)
}

// Counters reports frame cache hits and misses so far.
func (c *Cache) Counters() (hits, misses uint64) {
	return uint64(c.hits.Load()), uint64(c.misses.Load())
}

// PoolStats reports buffer requests, reuses and idle pooled bytes (statistics, tests).
func (c *Cache) PoolStats() (gets, reuses uint64, idleBytes int64) { return c.pool.Stats() }
