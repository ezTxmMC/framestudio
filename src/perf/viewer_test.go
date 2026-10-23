package perf

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	xdraw "golang.org/x/image/draw"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/viewer"
)

func runDur() time.Duration {
	if s := os.Getenv("FS_PERF_DUR"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 4 * time.Second
}

// paintEmu does on the calling (UI) goroutine what fyne's GL painter does with a
// canvas.Image holding an *image.NRGBA before glTexImage2D: software CatmullRom scale when
// the widget's texture size differs from the image (never upscaling) and an NRGBA->RGBA
// draw.Draw (internal/painter.PaintImage + gl.imgToTexture in fyne v2.8.2). The GL upload
// and the compositing of the rest of the window are not included.
func paintEmu(img *image.NRGBA, ww, wh int) {
	var im image.Image = img
	b := img.Bounds()
	if b.Dx() != ww || b.Dy() != wh {
		pw, ph := min(ww, b.Dx()), min(wh, b.Dy())
		tex := image.NewNRGBA(image.Rect(0, 0, pw, ph))
		xdraw.CatmullRom.Scale(tex, tex.Rect, img, b, xdraw.Over, nil)
		im = tex
	}
	rgba := image.NewRGBA(image.Rect(0, 0, im.Bounds().Dx(), im.Bounds().Dy()))
	draw.Draw(rgba, rgba.Rect, im, im.Bounds().Min, draw.Over)
}

type vres struct {
	name                   string
	rw, rh                 int // render size while playing
	ticks, shown, rendered int
	stale                  stats // frames the shown picture is behind the playhead, as ms
	gap                    stats // time between two shown pictures
	clock                  stats // time between playhead updates
	render                 stats // render duration per frame
	uiBatch                stats // UI thread time per drained batch
	uiBusy                 time.Duration
	qlat, framelat         stats // kit.Do latency of probe posts / of frame hand-offs
	gc                     gcSample
	dur                    time.Duration
	endFrame               int
	wtexW, wtexH           int
	scrubLat               stats
}

func (r vres) log(t *testing.T) {
	period := 1000.0 / 30
	t.Logf("VIEWER %-34s widget %dx%d render %dx%d | clock ticks=%d shown=%d (%.1f fps) rendered=%d | stale(ms): p50=%.0f p95=%.0f max=%.0f; frames shown >2 periods stale=%d | gap between pictures: p50=%.0f p95=%.0f max=%.0f ms; gaps>100ms=%d\n"+
		"      render per frame: %s\n      clock interval: %s\n      UI busy %.1f%% of wall; batches: %s\n      kit.Do latency probes (4 ms cadence): %s\n      frame hand-off latency: %s\n      %s",
		r.name, r.wtexW, r.wtexH, r.rw, r.rh, r.ticks, r.shown, float64(r.shown)/r.dur.Seconds(), r.rendered,
		r.stale.pct(.5), r.stale.pct(.95), r.stale.max(), r.stale.over(2*period),
		r.gap.pct(.5), r.gap.pct(.95), r.gap.max(), r.gap.over(100),
		r.render.String(), r.clock.String(), 100*float64(r.uiBusy)/float64(r.dur), r.uiBatch.String(), r.qlat.String(), r.framelat.String(), r.gc.String())
}

// runViewer plays p for dur through the real pipeline (kit.DrivePlayback clock, viewer
// render worker, real render.Renderer with media cache) with the test goroutine acting
// as the UI thread, and reports delivery, staleness and UI-thread numbers.
func runViewer(t *testing.T, name string, p *model.Project, winW, winH float32, quality string, emu bool, dur time.Duration) vres {
	return runViewerScript(t, name, p, winW, winH, quality, emu, dur, nil)
}

// harness gives a script control of the UI thread (the test goroutine).
type harness struct {
	s     *session.Session
	q     *kit.UIQueue
	res   *vres
	mu    sync.Mutex
	reqAt map[int]time.Time // when a frame was asked for (Seek)
	shown map[int]time.Time // when a picture of that frame reached the UI thread
	lat   stats             // request-to-picture latency of the pictures shown
	cache *media.Cache
}

func (h *harness) seek(f int) {
	h.mu.Lock()
	h.reqAt[f] = time.Now()
	h.mu.Unlock()
	h.s.Seek(f)
}

// pump runs UI callbacks for d.
func (h *harness) pump(d time.Duration) {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		a := time.Now()
		if n := h.q.Drain(); n > 0 {
			x := time.Since(a)
			h.res.uiBatch.add(x)
			h.res.uiBusy += x
		}
		time.Sleep(300 * time.Microsecond)
	}
}

// waitShown pumps until a picture of frame f was shown and returns the time since from.
func (h *harness) waitShown(f int, from time.Time, limit time.Duration) (time.Duration, bool) {
	end := time.Now().Add(limit)
	for time.Now().Before(end) {
		h.mu.Lock()
		at, ok := h.shown[f]
		h.mu.Unlock()
		if ok {
			return at.Sub(from), true
		}
		h.pump(time.Millisecond)
	}
	return limit, false
}

func runViewerScript(t *testing.T, name string, p *model.Project, winW, winH float32, quality string, emu bool, dur time.Duration, script func(h *harness)) vres {
	t.Helper()
	app := test.NewTempApp(t)
	q := kit.UseUIQueue(t)
	app.Settings().SetTheme(studiotheme.New())
	_ = settings.Update(func(s *settings.Settings) { *s = settings.Defaults() })
	s := session.New(p)
	cache := media.NewCache()
	defer cache.Close()
	rend := render.New(cache)
	v := viewer.New(s, rend)
	defer v.Close()
	v.SetQuality(quality)

	var res vres
	res.name, res.dur = name, dur
	res.wtexW, res.wtexH = 0, 0
	var mu sync.Mutex
	tags := map[*image.NRGBA]int{}
	var rendered atomic.Int64
	v.Source = func(pr *model.Project, f, w, h int, o render.Options) *image.NRGBA {
		a := time.Now()
		img := rend.FrameWith(pr, f, w, h, o)
		d := time.Since(a)
		mu.Lock()
		tags[img] = f
		res.render.add(d)
		mu.Unlock()
		rendered.Add(1)
		return img
	}
	v.SetDeliver(func(f func()) {
		t0 := time.Now()
		kit.Do(func() { res.framelat.add(time.Since(t0)); f() })
	})
	w := test.NewTempWindow(t, container.NewBorder(nil, viewer.Transport(s), nil, nil, v))
	w.Resize(fyne.NewSize(winW, winH))
	q.Settle(50*time.Millisecond, 2*time.Second)
	// the widget size a paused (full quality) frame has: what the painter textures against
	// (the render size is capped at the project resolution, the widget is not: ask with
	// a temporarily huge project to get the widget's pixel size)
	ow, oh := p.Settings.Width, p.Settings.Height
	p.Settings.Width, p.Settings.Height = ow*4, oh*4
	s.Notify(session.ChangeProject)
	pw, ph := v.RenderSize()
	res.wtexW, res.wtexH = pw, ph
	p.Settings.Width, p.Settings.Height = ow, oh
	s.Notify(session.ChangeProject)
	q.Settle(20*time.Millisecond, time.Second)

	h := &harness{cache: cache, s: s, q: q, res: &res, reqAt: map[int]time.Time{}, shown: map[int]time.Time{}}
	var lastShown time.Time
	var lastTick time.Time
	shownFrame := 0
	v.OnFrame = func(img *image.NRGBA) {
		now := time.Now()
		res.shown++
		if !lastShown.IsZero() {
			res.gap.add(now.Sub(lastShown))
		}
		lastShown = now
		mu.Lock()
		f := tags[img]
		delete(tags, img)
		mu.Unlock()
		shownFrame = f
		h.mu.Lock()
		if _, dup := h.shown[f]; !dup {
			h.shown[f] = now
			if at, ok := h.reqAt[f]; ok {
				h.lat.add(now.Sub(at))
			}
		}
		h.mu.Unlock()
		behind := s.Playhead - f
		res.stale.add(time.Duration(float64(behind) * float64(time.Second) / s.FPS().Float()))
		if emu {
			paintEmu(img, pw, ph)
		}
		res.rw, res.rh = img.Bounds().Dx(), img.Bounds().Dy()
	}
	unsub := s.Subscribe(func(c session.Change) {
		if c&session.ChangePlayhead != 0 {
			now := time.Now()
			res.ticks++
			if !lastTick.IsZero() {
				res.clock.add(now.Sub(lastTick))
			}
			lastTick = now
		}
	})
	defer unsub()
	stopClock := kit.DrivePlayback(s)
	defer stopClock()

	settle()
	g0 := readGC()
	// probes: post a no-op to the UI thread every 4 ms and measure how long it waits
	stopProbe := make(chan struct{})
	var pwg sync.WaitGroup
	pwg.Add(1)
	go func() {
		defer pwg.Done()
		tk := time.NewTicker(4 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stopProbe:
				return
			case <-tk.C:
				t0 := time.Now()
				kit.Do(func() { res.qlat.add(time.Since(t0)) })
			}
		}
	}()

	if script != nil {
		script(h)
		res.scrubLat = h.lat
	} else {
		s.Seek(0)
		s.SetPlaying(true)
		start := time.Now()
		for time.Since(start) < dur && s.Playing {
			a := time.Now()
			if n := q.Drain(); n > 0 {
				d := time.Since(a)
				res.uiBatch.add(d)
				res.uiBusy += d
			}
			time.Sleep(300 * time.Microsecond)
		}
	}
	res.gc = readGC().since(g0)
	res.endFrame = s.Playhead
	close(stopProbe)
	pwg.Wait()
	s.SetPlaying(false)
	_ = shownFrame
	res.rendered = int(rendered.Load())
	q.Settle(20*time.Millisecond, time.Second)
	return res
}

type vscen struct {
	name  string
	build func(t testing.TB) *model.Project
}

func viewerScenarios() []vscen {
	return []vscen{
		{"1clip 1080p30 longGOP", func(t testing.TB) *model.Project { return scenario1(t, H264_1080p30, 1920, 1080, model.Rate30) }},
		{"1clip 4K 80Mbit in 1080p proj", func(t testing.TB) *model.Project { return scenario1(t, H264_4K30HB, 1920, 1080, model.Rate30) }},
		{"3 layers+title+grade 1080p", func(t testing.TB) *model.Project { return scenario3(t, 1920, 1080, true) }},
		{"1clip 1080p59.94", func(t testing.TB) *model.Project {
			return scenario1(t, H264_1080p5994, 1920, 1080, model.Rate{Num: 60000, Den: 1001})
		}},
		{"4K proj 3x4K layers+title+grade", func(t testing.TB) *model.Project {
			p := newProject(3840, 2160, model.Rate30)
			ensureVideoTracks(p, 3)
			c := addVideo(p, 0, probe(t, H264_4K30HB))
			c.Grade = model.Grade{Exposure: 0.2, Contrast: 0.15, Saturation: 0.2, Vignette: 0.3}
			c2 := addVideo(p, 1, probe(t, HEVC_4K30))
			c2.Opacity = 0.6
			addVideo(p, 2, probe(t, H264_4K30))
			return p
		}},
	}
}

func viewerMatrix(t *testing.T, only string, windows [][2]float32, qualities []string, emu bool) {
	need(t)
	for _, sc := range viewerScenarios() {
		if only != "" && sc.name[:len(only)] != only {
			continue
		}
		for _, wsz := range windows {
			for _, ql := range qualities {
				p := sc.build(t)
				r := runViewer(t, sc.name+" win="+strconv.Itoa(int(wsz[0]))+"x"+strconv.Itoa(int(wsz[1]))+" q="+ql, p, wsz[0], wsz[1], ql, emu, runDur())
				r.log(t)
			}
		}
	}
}

// TestPerfViewerPlayback: viewer end to end, 1x playback from a cold cache. Window
// 1280x760 (render ~1236x695) and a big 2560x1440 monitor window, quality auto and full,
// with the fyne painter hand-off emulated on the UI thread.
func TestPerfViewerPlayback(t *testing.T) {
	viewerMatrix(t, os.Getenv("FS_PERF_SCEN"), [][2]float32{{1280, 760}, {2560, 1440}}, []string{viewer.QualityAuto, viewer.QualityFull}, true)
}

// TestPerfViewerPlaybackNoPaint is the same without the painter emulation: the application's
// own UI-thread work only.
func TestPerfViewerPlaybackNoPaint(t *testing.T) {
	viewerMatrix(t, os.Getenv("FS_PERF_SCEN"), [][2]float32{{1280, 760}}, []string{viewer.QualityAuto}, false)
}

// TestPerfViewerScrub drags the playhead through the real viewer: forward and backward
// drags (whole clip in 90 events, ~2 frames each), random jumps; reports the request-to-picture
// latency of the pictures that got shown, how many of the requests were skipped
// (latest wins) and how long after the last event the final picture appeared.
func TestPerfViewerScrub(t *testing.T) {
	need(t)
	for _, sc := range []struct {
		name string
		file string
	}{{"4K 80Mbit longGOP", H264_4K30HB}, {"4K longGOP", H264_4K30}, {"1080p longGOP", H264_1080p30}, {"1080p intra", Intra_1080p30}} {
		for _, mode := range scrubModes() {
			p := scenario1(t, sc.file, 1920, 1080, model.Rate30)
			total := p.Duration() - 3
			res := runViewerScript(t, sc.name+" "+mode, p, 1280, 760, viewer.QualityAuto, true, 0, func(h *harness) {
				h.seek(total / 2)
				h.waitShown(total/2, time.Now(), 3*time.Second) // first paint, decoder warm-up excluded
				h.res.scrubLat = stats{}
				h.lat = stats{}
				rnd := uint32(12345)
				req := 0
				last := 0
				t0 := time.Now()
				for i := 0; i < 90; i++ { // ~1.5 s of mouse events at 60 Hz
					var f int
					switch mode {
					case "drag fwd":
						f = 1 + i*(total-2)/89
					case "drag back":
						f = total - 1 - i*(total-2)/89
					default:
						rnd = rnd*1664525 + 1013904223
						f = int(rnd>>8) % total
					}
					h.seek(f)
					req++
					last = f
					h.pump(16 * time.Millisecond)
				}
				fin, ok := h.waitShown(last, time.Now(), 15*time.Second)
				_ = t0
				h.mu.Lock()
				n := h.lat.n()
				h.mu.Unlock()
				t.Logf("SCRUB-VIEWER %-30s %-10s requests=%d pictures shown=%d (skipped %d); request->picture: %s; final picture %.0f ms after last event (shown=%v)",
					sc.name, mode, req, n, req-n, h.lat.String(), float64(fin)/1e6, ok)
			})
			_ = res
		}
	}
}

// TestPerfViewerShuttle plays J/K/L at 2x, 4x, 8x forward and reverse through the viewer
// (shuttle preview size) and reports delivered pictures against the playhead.
func TestPerfViewerShuttle(t *testing.T) {
	need(t)
	for _, sc := range []struct {
		name string
		file string
	}{{"4K 80Mbit longGOP", H264_4K30HB}, {"1080p longGOP", H264_1080p30}} {
		for _, dir := range []int{1, -1} {
			for _, steps := range []int{1, 2, 3} { // 2x/1x..., with dir=1: 2x? first press = 1x
				p := scenario1(t, sc.file, 1920, 1080, model.Rate30)
				total := p.Duration() - 3
				name := fmt.Sprintf("%s dir=%d presses=%d", sc.name, dir, steps)
				var rate float64
				res := runViewerScript(t, name, p, 1280, 760, viewer.QualityAuto, true, 0, func(h *harness) {
					if dir < 0 {
						h.seek(total - 2)
						h.pump(200 * time.Millisecond)
					}
					for i := 0; i < steps; i++ {
						h.s.Shuttle(dir)
					}
					rate = h.s.Rate
					h.res.shown = 0
					h.res.stale, h.res.gap = stats{}, stats{}
					h.pump(runDur() / 2 * 1)
					h.s.SetPlaying(false)
				})
				res.dur = runDur() / 2
				t.Logf("SHUTTLE-VIEWER %s rate=%+g: pictures shown=%d (%.1f/s), stale p50=%.0f p95=%.0f max=%.0f ms; gap p50=%.0f p95=%.0f max=%.0f ms; render/frame: %s",
					name, rate, res.shown, float64(res.shown)/res.dur.Seconds(), res.stale.pct(.5), res.stale.pct(.95), res.stale.max(),
					res.gap.pct(.5), res.gap.pct(.95), res.gap.max(), res.render.String())
			}
		}
	}
}

func scrubModes() []string {
	if m := os.Getenv("FS_PERF_MODE"); m != "" {
		return []string{m}
	}
	return []string{"drag fwd", "drag back", "random"}
}

// TestPerfBackgroundContention plays 3 layers while the media cache serves a burst of
// timeline-style background work (filmstrip posters of other 4K files, a waveform), as
// it happens after dropping clips on the timeline or opening the media page. Compare
// the "idle" and "with background" rows: render time and delivered pictures.
func TestPerfBackgroundContention(t *testing.T) {
	need(t)
	other := probe(t, H264_4K30HB)
	other2 := probe(t, HEVC_4K30)
	for _, bg := range []bool{false, true} {
		p := scenario3(t, 1920, 1080, true)
		name := "idle"
		if bg {
			name = "with background posters"
		}
		res := runViewerScript(t, name, p, 1280, 760, viewer.QualityAuto, false, 0, func(h *harness) {
			stop := make(chan struct{})
			var wg sync.WaitGroup
			var done atomic.Int64
			if bg {
				for g := 0; g < 6; g++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for i := 0; ; i++ {
							select {
							case <-stop:
								return
							default:
							}
							m := other
							if (i+g)%2 == 1 {
								m = other2
							}
							if _, err := h.cache.Poster(m, float64((i*7+g*13)%50)/10, 160, 90); err == nil {
								done.Add(1)
							}
						}
					}()
				}
			}
			h.res.shown = 0
			h.s.Seek(0)
			h.s.SetPlaying(true)
			h.pump(runDur())
			h.s.SetPlaying(false)
			close(stop)
			wg.Wait()
			t.Logf("   background posters completed during playback: %d", done.Load())
		})
		res.dur = runDur()
		t.Logf("BG %-26s shown=%d (%.1f fps); stale p95=%.0f max=%.0f ms; gap p95=%.0f max=%.0f; render/frame: %s",
			name, res.shown, float64(res.shown)/res.dur.Seconds(), res.stale.pct(.95), res.stale.max(), res.gap.pct(.95), res.gap.max(), res.render.String())
	}
}
