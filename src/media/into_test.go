package media

import (
	"bytes"
	"context"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"framestudio-proto/src/media/hwaccel"
	"framestudio-proto/src/media/proxy"
	"framestudio-proto/src/model"
	"framestudio-proto/src/workgate"
)

// childProcesses lists the ffmpeg/ffprobe children of this process.
func childProcesses() []string {
	me := os.Getpid()
	ents, _ := os.ReadDir("/proc")
	var out []string
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(b)
		i, j := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if i < 0 || j < i {
			continue
		}
		f := strings.Fields(s[j+2:])
		if len(f) < 2 {
			continue
		}
		if ppid, _ := strconv.Atoi(f[1]); ppid == me {
			comm := s[i+1 : j]
			if strings.HasPrefix(comm, "ffmpeg") || strings.HasPrefix(comm, "ffprobe") {
				out = append(out, comm+"/"+e.Name())
			}
		}
	}
	return out
}

func noChildren(t *testing.T) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for {
		ch := childProcesses()
		if len(ch) == 0 {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("ffmpeg processes leaked: %v", ch)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBufPool(t *testing.T) {
	p := NewBufPool(1 << 20)
	a := p.Get(1000)
	p.Put(a)
	b := p.Get(1000)
	if &a[0] != &b[0] {
		t.Fatal("buffer not reused")
	}
	p.Put(b)
	p.Put(make([]byte, 2<<20)) // over the cap: dropped
	if _, _, idle := p.Stats(); idle != 1000 {
		t.Fatalf("idle %d", idle)
	}
	p.SetLimit(10)
	if _, _, idle := p.Stats(); idle != 0 {
		t.Fatalf("limit did not trim: %d", idle)
	}
}

func TestPooledFrameRefCounts(t *testing.T) {
	l := newLRU(300)
	pool := NewBufPool(1 << 20)
	mk := func(key string) *pframe {
		pf := pool.newFrame(100)
		l.putPF(key, "o", rgbaFrame(pf.buf, 5, 5), 100, pf, SrcSW)
		return pf
	}
	a := mk("a")
	a.release() // producer done: only the cache holds it
	_, _, rel, ok := l.getRef("a")
	if !ok {
		t.Fatal("miss")
	}
	l.dropOwner("o") // evicted while a reader looks at it
	if _, _, idle := pool.Stats(); idle != 0 {
		t.Fatal("buffer recycled under a reader")
	}
	rel()
	if _, _, idle := pool.Stats(); idle != 100 {
		t.Fatalf("buffer not returned after the last reference: %d", idle)
	}
	// get (the plain API) lets the buffer escape: never recycled
	b := mk("b")
	b.release()
	if _, ok := l.get("b"); !ok {
		t.Fatal("miss")
	}
	l.dropOwner("o")
	if _, _, idle := pool.Stats(); idle != 0 { // b took the idle buffer of a and keeps it
		t.Fatalf("escaped buffer was recycled: %d", idle)
	}
}

func TestVideoFrameIntoMatchesAndPools(t *testing.T) {
	m := rateVideo(t, 25, 6)
	c := NewCache()
	defer c.Close()
	ref := NewCache()
	defer ref.Close()
	c.Budget(40 * 64 * 36 * 4) // ~40 frames: playback evicts and recycles
	ctx := context.Background()
	for n := 0; n < 140; n++ {
		d, err := c.VideoFrameInto(ctx, m, model.Rate25.FramesToDuration(n), model.Rate25, 64, 36, MediaHint{Speed: 1})
		if err != nil {
			t.Fatal(err)
		}
		if d.W != 64 || d.H != 36 || d.Stride != 64*4 || d.Approx {
			t.Fatalf("frame %d: %+v", n, d)
		}
		want, err := ref.VideoFrame(m, n, model.Rate25, 64, 36)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(d.Pix, pixOf(want)) {
			t.Fatalf("frame %d differs from VideoFrame", n)
		}
		d.Release()
	}
	gets, reuses, _ := c.PoolStats()
	if gets < 100 || reuses < 40 {
		t.Fatalf("steady playback should recycle buffers: gets=%d reuses=%d", gets, reuses)
	}
	c.Close()
	noChildren(t)
}

func TestKeyframeSeekMatchesDefaultSeek(t *testing.T) {
	m := rateVideo(t, 25, 10) // keyframe every second
	withIdx := NewCache()
	defer withIdx.Close()
	plain := NewCache()
	defer plain.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	times, ok := withIdx.KeyframeTimes(ctx, m)
	if !ok || len(times) != 10 || times[0] != 0 || times[1] != time.Second {
		t.Fatalf("keyframe index %v ok=%v", times, ok)
	}
	// a second index lookup is a cache hit
	if again, _ := withIdx.KeyframeTimes(ctx, m); len(again) != len(times) {
		t.Fatal("index changed")
	}
	e := plain.kf.entry(m) // plain never uses an index
	e.once.Do(func() {})
	close(e.ready)
	for _, n := range []int{0, 1, 24, 25, 26, 99, 100, 101, 187, 249} {
		a, err := withIdx.VideoFrame(m, n, model.Rate25, 64, 36)
		if err != nil {
			t.Fatal(err)
		}
		b, err := plain.VideoFrame(m, n, model.Rate25, 64, 36)
		if err != nil {
			t.Fatal(err)
		}
		if !sameImage(a, b) {
			t.Fatalf("frame %d differs with keyframe seek", n)
		}
		// random access must still match after a jump (new decoder per jump)
		withIdx.stopPrefetch("")
		plain.stopPrefetch("")
	}
	o, hasSeek := withIdx.keyframeSeek(m, model.Rate25, 60)
	if !hasSeek || o < 2*time.Second || o > 2*time.Second+time.Millisecond {
		t.Fatalf("seek for frame 60 = %v %v, want keyframe at 2.4 s -> 2 s", o, hasSeek)
	}
}

func TestExactShuttleNeverBlocks(t *testing.T) {
	m := rateVideo(t, 25, 12)
	for _, rate := range []float64{4, 8, -4, -8} {
		c := NewCache()
		start := 0
		if rate < 0 {
			start = 280
		}
		ctx := context.Background()
		d, err := c.VideoFrameInto(ctx, m, model.Rate25.FramesToDuration(start), model.Rate25, 64, 36, MediaHint{Speed: rate})
		if err != nil {
			t.Fatal(err)
		}
		d.Release()
		slowest := time.Duration(0)
		n := start
		exact := 0
		for i := 0; i < 25; i++ {
			n += int(rate)
			t0 := time.Now()
			d, err := c.VideoFrameInto(ctx, m, model.Rate25.FramesToDuration(n), model.Rate25, 64, 36, MediaHint{Speed: rate})
			slowest = max(slowest, time.Since(t0))
			if err != nil {
				t.Fatalf("rate %v frame %d: %v", rate, n, err)
			}
			if !d.Approx {
				exact++
			}
			if d.KFOnly {
				t.Fatalf("rate %v: keyframe-only although decoding is fast enough", rate)
			}
			d.Release()
			time.Sleep(15 * time.Millisecond)
		}
		if slowest > 400*time.Millisecond {
			t.Fatalf("rate %v: a request blocked %v", rate, slowest)
		}
		if exact < 5 {
			t.Fatalf("rate %v: only %d of 25 pictures were exact", rate, exact)
		}
		if live := c.LiveDecoders(); live > c.DecoderLimit() {
			t.Fatalf("%d decoders", live)
		}
		c.Close()
	}
	noChildren(t)
}

func TestScrubApproxWithinDeadline(t *testing.T) {
	m := rateVideo(t, 25, 20) // 500 frames, keyframe every second
	c := NewCache()
	defer c.Close()
	ctx := context.Background()
	if _, err := c.VideoFrameInto(ctx, m, 0, model.Rate25, 64, 36, MediaHint{Scrub: true}); err != nil {
		t.Fatal(err)
	}
	end := time.Now().Add(10 * time.Second)
	for c.ladderFor(m) == nil {
		if time.Now().After(end) {
			t.Fatal("keyframe ladder never built")
		}
		time.Sleep(10 * time.Millisecond)
	}
	l := c.ladderFor(m)
	if len(l.imgs) != 20 {
		t.Fatalf("ladder has %d pictures", len(l.imgs))
	}
	// A far jump with a deadline in the past answers at once from the ladder.
	t0 := time.Now()
	d, err := c.VideoFrameInto(ctx, m, 13*time.Second, model.Rate25, 64, 36, MediaHint{Scrub: true, Deadline: time.Now()})
	took := time.Since(t0)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Approx || d.W == 0 || d.W > ladderW {
		t.Fatalf("expected an approximate ladder picture, got %+v", d)
	}
	if took > 30*time.Millisecond {
		t.Fatalf("approximate picture took %v", took)
	}
	d.Release()
	// The exact frame keeps decoding: asking again without a deadline is exact.
	d, err = c.VideoFrameInto(ctx, m, 13*time.Second, model.Rate25, 64, 36, MediaHint{Scrub: true})
	if err != nil || d.Approx || d.W != 64 {
		t.Fatalf("exact frame: %+v %v", d, err)
	}
	d.Release()
}

func TestIntoCancelled(t *testing.T) {
	m := rateVideo(t, 25, 6)
	c := NewCache()
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.VideoFrameInto(ctx, m, time.Second, model.Rate25, 64, 36, MediaHint{}); err == nil {
		t.Fatal("cancelled request succeeded")
	}
}

// proxyFor builds a proxy of m through the real manager.
func makeProxy(t *testing.T, m model.MediaItem) *proxy.Manager {
	t.Helper()
	pm := proxy.New(proxy.Config{Dir: filepath.Join(t.TempDir(), "p"), Mode: proxy.ModeAlways, IdleDelay: time.Millisecond})
	t.Cleanup(pm.Close)
	pm.Want(m.Path)
	end := time.Now().Add(30 * time.Second)
	for {
		if _, ok := pm.Lookup(m.Path); ok {
			return pm
		}
		if st := pm.Status(m.Path); st.State == proxy.StateFailed || time.Now().After(end) {
			t.Fatalf("proxy not built: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestProxyUsedTransparentlyAndNotForAccurate(t *testing.T) {
	m := rateVideo(t, 25, 4)
	pm := makeProxy(t, m)
	c := NewCache()
	defer c.Close()
	c.SetProxies(pm)
	ctx := context.Background()
	at := model.Rate25.FramesToDuration(40)

	d, err := c.VideoFrameInto(ctx, m, at, model.Rate25, 64, 36, MediaHint{Scrub: true})
	if err != nil || d.Src != SrcProxy {
		t.Fatalf("scrub should use the proxy: %+v %v", d, err)
	}
	d.Release()
	d, err = c.VideoFrameInto(ctx, m, at, model.Rate25, 64, 36, MediaHint{Accurate: true})
	if err != nil || d.Src != SrcSW {
		t.Fatalf("accurate requests use the original: %+v %v", d, err)
	}
	d.Release()
	// The plain API (export) never sees proxies.
	img, err := c.VideoFrame(m, 40, model.Rate25, 64, 36)
	if err != nil {
		t.Fatal(err)
	}
	orig := NewCache()
	defer orig.Close()
	want, _ := orig.VideoFrame(m, 40, model.Rate25, 64, 36)
	if !sameImage(img, want) {
		t.Fatal("VideoFrame returned proxy pixels")
	}
	// Normal playback of a small clip at full quality in a big viewer stays on the original
	// in auto mode; here the mode is "always", so the proxy serves it.
	pm.SetMode(proxy.ModeAuto)
	d, err = c.VideoFrameInto(ctx, m, model.Rate25.FramesToDuration(80), model.Rate25, 640, 360, MediaHint{Speed: 1, RenderH: 1080})
	if err != nil || d.Src != SrcSW {
		t.Fatalf("1x at 1080 render height should use the original: %+v %v", d, err)
	}
	d.Release()
	d, err = c.VideoFrameInto(ctx, m, model.Rate25.FramesToDuration(81), model.Rate25, 64, 36, MediaHint{Speed: 1, RenderH: 360})
	if err != nil || d.Src != SrcProxy {
		t.Fatalf("small render height should use the proxy: %+v %v", d, err)
	}
	d.Release()
	d, err = c.VideoFrameInto(ctx, m, model.Rate25.FramesToDuration(20), model.Rate25, 64, 36, MediaHint{Speed: -2, RenderH: 1080})
	if err != nil || d.Src != SrcProxy {
		t.Fatalf("reverse should use the proxy: %+v %v", d, err)
	}
	d.Release()
}

func TestProxyCorruptFallsBackToOriginal(t *testing.T) {
	m := rateVideo(t, 25, 4)
	pm := makeProxy(t, m)
	info, _ := pm.Lookup(m.Path)
	if err := os.WriteFile(info.Path, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCache()
	defer c.Close()
	c.SetProxies(pm)
	d, err := c.VideoFrameInto(context.Background(), m, model.Rate25.FramesToDuration(30), model.Rate25, 64, 36, MediaHint{Scrub: true})
	if err != nil {
		t.Fatalf("corrupt proxy must fall back: %v", err)
	}
	if d.Src != SrcSW {
		t.Fatalf("source %v", d.Src)
	}
	d.Release()
	d, err = c.VideoFrameInto(context.Background(), m, model.Rate25.FramesToDuration(60), model.Rate25, 64, 36, MediaHint{Scrub: true})
	if err != nil || d.Src != SrcSW {
		t.Fatalf("bad proxy is skipped afterwards: %+v %v", d, err)
	}
	d.Release()
	// missing proxy file
	m2 := rateVideo(t, 25, 3)
	pm2 := makeProxy(t, m2)
	info2, _ := pm2.Lookup(m2.Path)
	_ = os.Remove(info2.Path)
	c.SetProxies(pm2)
	if d, err := c.VideoFrameInto(context.Background(), m2, time.Second, model.Rate25, 64, 36, MediaHint{Scrub: true}); err != nil || d.Src != SrcSW {
		t.Fatalf("missing proxy: %+v %v", d, err)
	}
}

// fakeHW plans a hardware decode that cannot work.
type fakeHW struct {
	mu    sync.Mutex
	fails int
	args  hwaccel.Args
}

func (f *fakeHW) Plan(string, hwaccel.Info, int, int) (hwaccel.Args, bool) { return f.args, true }
func (f *fakeHW) Fail(string, hwaccel.Info) {
	f.mu.Lock()
	f.fails++
	f.mu.Unlock()
}
func (f *fakeHW) Info() hwaccel.Result {
	return hwaccel.Result{Kind: hwaccel.VAAPI, Device: "/dev/dri/none", Codecs: map[string]bool{"h264": true}}
}

func TestHardwareFailureFallsBackToSoftware(t *testing.T) {
	m := rateVideo(t, 25, 4)
	f := &fakeHW{args: hwaccel.Args{
		Input:  []string{"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/does-not-exist", "-hwaccel_output_format", "vaapi"},
		Filter: "hwdownload,format=nv12"}}
	c := NewCache()
	defer c.Close()
	c.SetHW(f)
	t0 := time.Now()
	d, err := c.VideoFrameInto(context.Background(), m, model.Rate25.FramesToDuration(10), model.Rate25, 64, 36, MediaHint{Speed: 1})
	if err != nil {
		t.Fatalf("hardware failure must be invisible: %v", err)
	}
	if time.Since(t0) > 3*time.Second {
		t.Fatalf("first frame took %v", time.Since(t0))
	}
	if d.Src != SrcSW {
		t.Fatalf("source %v after fallback", d.Src)
	}
	d.Release()
	f.mu.Lock()
	fails := f.fails
	f.mu.Unlock()
	if fails != 1 {
		t.Fatalf("failure reported %d times", fails)
	}
	// frames from the software run are right
	ref := NewCache()
	defer ref.Close()
	want, _ := ref.VideoFrame(m, 11, model.Rate25, 64, 36)
	got, err := c.VideoFrame(m, 11, model.Rate25, 64, 36)
	if err != nil || !sameImage(got, want) {
		t.Fatal("software fallback pictures differ")
	}
	c.Close()
	noChildren(t)
}

func TestRealHardwareDecodeIfAvailable(t *testing.T) {
	needFFmpeg(t)
	pr := &hwaccel.Prober{CacheDir: t.TempDir()}
	hm := hwaccel.NewManager(pr)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	res := hm.ProbeNow(ctx)
	if res.Kind == hwaccel.None || !res.Codecs["h264"] {
		t.Skip("no hardware H.264 decoding on this machine")
	}
	path := filepath.Join(t.TempDir(), "hd.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=25:duration=3", "-c:v", "libx264",
		"-preset", "ultrafast", "-g", "50", "-pix_fmt", "yuv420p", path)
	m := mustProbe(t, path)
	hwc := NewCache()
	defer hwc.Close()
	hwc.SetHW(hm)
	swc := NewCache()
	defer swc.Close()
	var hwOK int
	for n := 0; n < 40; n++ {
		a, err := hwc.VideoFrameInto(context.Background(), m, model.Rate25.FramesToDuration(n), model.Rate25, 960, 540, MediaHint{Speed: 1})
		if err != nil {
			t.Fatal(err)
		}
		if a.Src == SrcHW {
			hwOK++
		}
		b, err := swc.VideoFrame(m, n, model.Rate25, 960, 540)
		if err != nil {
			t.Fatal(err)
		}
		bp := pixOf(b)
		if len(bp) != len(a.Pix) {
			t.Fatalf("size mismatch")
		}
		var diff int64
		for i := 0; i < len(bp); i += 4 {
			for k := 0; k < 3; k++ {
				d := int(bp[i+k]) - int(a.Pix[i+k])
				diff += int64(d * d)
			}
		}
		mse := float64(diff) / float64(len(bp)/4*3)
		if mse > 60 { // hardware scaling differs a little from swscale
			t.Fatalf("frame %d: hardware picture differs, mse %.1f", n, mse)
		}
		a.Release()
	}
	if hwOK == 0 {
		t.Fatalf("hardware probe says %v but no frame was hardware decoded", res.Kind)
	}
	t.Logf("hardware decode (%s): %d of 40 frames", res.Kind, hwOK)
}

func TestBackgroundWorkWaitsForPlayback(t *testing.T) {
	m := rateVideo(t, 25, 6)
	c := NewCache()
	defer c.Close()
	g := workgate.New()
	defer g.Close()
	c.SetGate(g)
	g.SetState(workgate.Playing)
	var wg sync.WaitGroup
	done := make(chan float64, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Poster(m, float64(i)*0.5, 32, 32); err != nil {
				t.Error(err)
			}
			done <- 1
		}()
	}
	time.Sleep(400 * time.Millisecond)
	if len(done) != 0 {
		t.Fatalf("%d posters finished while playing", len(done))
	}
	if kids := childProcesses(); len(kids) != 0 {
		t.Fatalf("ffmpeg started while playing: %v", kids)
	}
	// playback decoders are not gated
	if _, err := c.VideoFrame(m, 5, model.Rate25, 64, 36); err != nil {
		t.Fatal(err)
	}
	g.SetState(workgate.Idle)
	ch := make(chan struct{})
	go func() { wg.Wait(); close(ch) }()
	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		t.Fatal("posters did not finish after playback stopped")
	}
}

func TestNoProcessLeaksAfterMixedUse(t *testing.T) {
	m := rateVideo(t, 25, 6)
	c := NewCache()
	ctx := context.Background()
	for _, n := range []int{0, 100, 30, 120, 5} {
		d, err := c.VideoFrameInto(ctx, m, model.Rate25.FramesToDuration(n), model.Rate25, 64, 36, MediaHint{Scrub: n%2 == 0})
		if err != nil {
			t.Fatal(err)
		}
		d.Release()
	}
	_, _ = c.PlaybackFrame(m, 140, model.Rate25, 64, 36, -4)
	_, _ = c.PlaybackFrame(m, 130, model.Rate25, 64, 36, 8)
	_, _ = c.Poster(m, 1, 32, 32)
	_, _ = c.Waveform(m, 10)
	if c.LiveDecoders() > c.DecoderLimit() {
		t.Fatalf("%d live decoders, limit %d", c.LiveDecoders(), c.DecoderLimit())
	}
	c.Close()
	noChildren(t)
}

func pixOf(img image.Image) []byte { return img.(*image.RGBA).Pix }
