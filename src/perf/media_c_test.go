package perf

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/media/hwaccel"
	"framestudio-proto/src/media/proxy"
	"framestudio-proto/src/model"
	"framestudio-proto/src/workgate"
)

// proxyFor builds (and waits for) the proxy of a test file in a private cache dir.
func proxyFor(t *testing.T, name string) (*proxy.Manager, model.MediaItem) {
	t.Helper()
	m := probe(t, name)
	pm := proxy.New(proxy.Config{Dir: filepath.Join(t.TempDir(), "proxies"), Mode: proxy.ModeAlways, IdleDelay: time.Millisecond})
	t.Cleanup(pm.Close)
	t0 := time.Now()
	pm.Want(m.Path)
	for {
		if _, ok := pm.Lookup(m.Path); ok {
			break
		}
		if st := pm.Status(m.Path); st.State == proxy.StateFailed || time.Since(t0) > 55*time.Second {
			t.Fatalf("proxy of %s: %+v", name, st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	info, _ := pm.Lookup(m.Path)
	t.Logf("PROXY %s built in %.1fs: %dx%d %d frames", name, time.Since(t0).Seconds(), info.W, info.H, info.Frames)
	return pm, m
}

// TestPerfScrubInto: dragging the playhead (60 requests/s, latest wins) with the scrub
// hint: originals vs proxy. Reports the share of drag positions that got a picture and the
// time to picture.
func TestPerfScrubInto(t *testing.T) {
	need(t)
	for _, name := range []string{H264_1080p30, H264_4K30HB} {
		pm, m := proxyFor(t, name)
		fps := fpsOf(m)
		total := fps.DurationToFrames(m.Duration) - 2
		for _, mode := range []string{"original", "proxy"} {
			for _, dir := range []string{"fwd", "back", "random"} {
				c := media.NewCache()
				if mode == "proxy" {
					c.SetProxies(pm)
				}
				rnd := rand.New(rand.NewSource(3))
				var lat stats
				pos, ok, approx := 0, 0, 0
				const positions = 90
				t0 := time.Now()
				drag := total / 4
				if dir == "back" {
					drag = total * 3 / 4
				}
				for pos < positions && time.Since(t0) < 12*time.Second {
					pos = int(time.Since(t0) / (16 * time.Millisecond))
					if pos >= positions {
						break
					}
					n := drag + pos*3
					switch dir {
					case "back":
						n = drag - pos*3
					case "random":
						n = rnd.Intn(total)
					}
					a := time.Now()
					d, err := c.VideoFrameInto(context.Background(), m, fps.FramesToDuration(n), fps, 1280, 720,
						media.MediaHint{Scrub: true, Speed: 0, RenderH: 360, Deadline: a.Add(33 * time.Millisecond)})
					if err != nil {
						t.Fatal(err)
					}
					lat.add(time.Since(a))
					ok++
					if d.Approx {
						approx++
					}
					d.Release()
					time.Sleep(time.Until(t0.Add(time.Duration(pos+1) * 16 * time.Millisecond)))
				}
				t.Logf("SCRUBINTO %-22s %-8s %-6s positions served %2d/%d (approx %d) time-to-picture: %s",
					name, mode, dir, ok, positions, approx, lat.String())
				c.Close()
			}
		}
	}
}

// shuttleInto: all shuttle speeds through VideoFrameInto, with or without proxy.
func shuttleInto(t *testing.T, name string, pm *proxy.Manager, prewarm bool) {
	m := probe(t, name)
	fps := fpsOf(m)
	total := fps.DurationToFrames(m.Duration) - 2
	label := "original"
	if pm != nil {
		label = "proxy"
	}
	if prewarm {
		label += "+prewarm"
	}
	for _, rate := range []float64{2, 4, 8, -1, -4, -8} {
		c := media.NewCache()
		if pm != nil {
			c.SetProxies(pm)
		}
		start := 0
		if rate < 0 {
			start = total - 3
		}
		if prewarm { // what the app does on project load and at every seek end
			c.Prewarm(m, fps.FramesToDuration(start), fps, 854, 480)
			time.Sleep(2500 * time.Millisecond)
		}
		var lat stats
		dist, last, kf := 0, any(nil), 0
		t0 := time.Now()
		var worstGap float64
		lastChange := t0
		for time.Since(t0) < 1500*time.Millisecond {
			n := start + int(rate*time.Since(t0).Seconds()*fps.Float())
			if n < 0 || n >= total {
				break
			}
			a := time.Now()
			d, err := c.VideoFrameInto(context.Background(), m, fps.FramesToDuration(n), fps, 854, 480, media.MediaHint{Speed: rate, RenderH: 480})
			if err != nil {
				t.Fatal(err)
			}
			lat.add(time.Since(a))
			if d.KFOnly {
				kf++
			}
			if p := &d.Pix[0]; any(p) != last {
				dist++
				worstGap = max(worstGap, float64(time.Since(lastChange))/1e6)
				lastChange = time.Now()
				last = any(p)
			}
			d.Release()
			time.Sleep(8 * time.Millisecond)
		}
		t.Logf("SHUTTLEINTO %-20s %-8s %+gx pictures=%3d (%.1f/s) keyframe-only calls=%d staleMax=%.0fms call: %s", name, label, rate, dist, float64(dist)/1.5, kf, worstGap, lat.String())
		c.Close()
	}
}

// TestPerfShuttleInto: shuttle on the originals through the new API (exact where decoding
// keeps up, keyframe ladder otherwise).
func TestPerfShuttleInto(t *testing.T) {
	need(t)
	for _, name := range []string{H264_1080p30, H264_4K30, H264_4K30HB} {
		shuttleInto(t, name, nil, false)
	}
	shuttleInto(t, H264_4K30HB, nil, true)
}

// TestPerfProxyShuttle: all shuttle speeds from a proxy.
func TestPerfProxyShuttle(t *testing.T) {
	need(t)
	pm, _ := proxyFor(t, H264_4K30HB)
	shuttleInto(t, H264_4K30HB, pm, false)
}

// cpuSeconds is the CPU time this process and its (waited-for) children used.
func cpuSeconds() float64 {
	var s, c syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &s)
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &c)
	tv := func(t syscall.Timeval) float64 { return float64(t.Sec) + float64(t.Usec)/1e6 }
	return tv(s.Utime) + tv(s.Stime) + tv(c.Utime) + tv(c.Stime)
}

// TestPerfHardwareDecodeCPU: CPU seconds for 120 frames of the 4K 80 Mbit file, software
// vs hardware (skipped without VAAPI/NVDEC).
func TestPerfHardwareDecodeCPU(t *testing.T) {
	need(t)
	hm := hwaccel.NewManager(&hwaccel.Prober{CacheDir: t.TempDir()})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if res := hm.ProbeNow(ctx); res.Kind == hwaccel.None || !res.Codecs["h264"] {
		t.Skip("no hardware H.264 decode")
	}
	m := probe(t, H264_4K30HB)
	fps := fpsOf(m)
	for _, mode := range []string{"software", "hardware"} {
		c := media.NewCache()
		if mode == "hardware" {
			c.SetHW(hm)
		}
		c0, t0 := cpuSeconds(), time.Now()
		var src media.SourceKind
		for n := 0; n < 120; n++ {
			d, err := c.VideoFrameInto(context.Background(), m, fps.FramesToDuration(n), fps, 1280, 720, media.MediaHint{Speed: 1})
			if err != nil {
				t.Fatal(err)
			}
			src = d.Src
			d.Release()
		}
		c.Close() // waits for the decoder processes so their CPU time is counted
		t.Logf("HWDECODE 4K 80Mbit %-8s 120 frames: wall %.2fs, CPU %.2f s (source %v)", mode, time.Since(t0).Seconds(), cpuSeconds()-c0, src)
	}
}

// TestPerfBackgroundGate: 6 poster jobs requested while "playing": playback decode time
// per frame with and without the gate.
func TestPerfBackgroundGate(t *testing.T) {
	need(t)
	m := probe(t, H264_1080p30HB)
	fps := fpsOf(m)
	others := []string{H264_4K30, HEVC_4K30, H264_4K30HB}
	run := func(gated bool) (stats, int) {
		c := media.NewCache()
		defer c.Close()
		g := workgate.New()
		defer g.Close()
		if gated {
			c.SetGate(g)
			g.SetState(workgate.Playing)
		}
		done := make(chan struct{}, 6)
		for i := 0; i < 6; i++ {
			o := probe(t, others[i%len(others)])
			go func() {
				_, _ = c.Poster(o, float64(i)*0.7, 160, 90)
				done <- struct{}{}
			}()
		}
		var st stats
		for n := 0; n < 120; n++ {
			a := time.Now()
			if _, err := c.VideoFrame(m, n, fps, 1280, 720); err != nil {
				t.Fatal(err)
			}
			st.add(time.Since(a))
		}
		finished := len(done)
		if gated {
			g.SetState(workgate.Idle)
		}
		for i := finished; i < 6; i++ {
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("posters never finished")
			}
		}
		return st, finished
	}
	base, _ := func() (stats, int) {
		c := media.NewCache()
		defer c.Close()
		var st stats
		for n := 0; n < 120; n++ {
			a := time.Now()
			if _, err := c.VideoFrame(m, n, fps, 1280, 720); err != nil {
				t.Fatal(err)
			}
			st.add(time.Since(a))
		}
		return st, 0
	}()
	free, f1 := run(false)
	gated, f2 := run(true)
	sum := func(s stats) float64 { return s.mean() * float64(s.n()) }
	t.Logf("BGGATE 120 frames decode, total wall: baseline %.0f ms | 6 posters ungated %.0f ms (%+.0f%%, posters done during playback %d) | gated %.0f ms (%+.0f%%, posters done during playback %d)",
		sum(base), sum(free), 100*(sum(free)/sum(base)-1), f1, sum(gated), 100*(sum(gated)/sum(base)-1), f2)
	_ = os.Stdout
}
