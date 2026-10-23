package perf

import (
	"image"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/viewer"
)

// Tests of the new preview pipeline that need no media: a synthetic video source
// stands in for the decoder so the numbers isolate compositor, hand-off and UI thread.

func perfOn(t *testing.T) {
	t.Helper()
	if os.Getenv("FS_PERF") == "" {
		t.Skip("set FS_PERF=1 to run the preview performance harness")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
}

// synthVideo serves the same RGBA picture for every frame (no decode cost).
type synthVideo struct {
	mu  sync.Mutex
	pic map[[2]int]*image.RGBA
	// spin burns CPU per megapixel requested, standing in for decode + scale work
	spin time.Duration
}

func (s *synthVideo) VideoFrame(m model.MediaItem, n int, fps model.Rate, w, h int) (image.Image, error) {
	if s.spin > 0 {
		for end := time.Now().Add(time.Duration(float64(s.spin) * float64(w*h) / 1e6)); time.Now().Before(end); {
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pic == nil {
		s.pic = map[[2]int]*image.RGBA{}
	}
	k := [2]int{w, h}
	if p := s.pic[k]; p != nil {
		return p, nil
	}
	p := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(p.Pix); i += 4 {
		p.Pix[i], p.Pix[i+1], p.Pix[i+2], p.Pix[i+3] = uint8(i), uint8(i>>6), 100, 255
	}
	s.pic[k] = p
	return p, nil
}

func synthProject(w, h int, rate model.Rate, layers int) *model.Project {
	p := newProject(w, h, rate)
	ensureVideoTracks(p, layers)
	for i := range layers {
		c := addVideo(p, i, model.MediaItem{ID: "m" + strconv.Itoa(i), Name: "v.mp4", Path: "/none/v.mp4",
			Kind: model.KindVideo, Width: w, Height: h, FPS: rate, Duration: 20 * time.Second})
		if i == 0 {
			c.Grade = model.Grade{Exposure: 0.1, Contrast: 0.1, Saturation: 0.1}
		} else {
			c.Opacity = 0.7
		}
	}
	return p
}

type uires struct {
	shown, fps                 float64
	busy                       float64 // fraction of wall time the UI goroutine ran callbacks
	batch, qlat                stats
	renderW, renderH           int
	dropped, late              uint64
	qualityChanges             int
	finalQ                     string
	staleMax                   float64
	oldPaint                   stats // emulated cost per frame of the old NRGBA/CatmullRom hand-off
	paintWidgetW, paintWidgetH int
}

func runPreviewUI(t *testing.T, winW, winH float32, p *model.Project, quality string, dur time.Duration, spin ...time.Duration) uires {
	t.Helper()
	app := test.NewTempApp(t)
	q := kit.UseUIQueue(t)
	app.Settings().SetTheme(studiotheme.New())
	_ = settings.Update(func(s *settings.Settings) {
		*s = settings.Defaults()
		s.Playback.MaxPreviewH = 0 // the ladder governs, no fixed cap
	})
	s := session.New(p)
	sv := &synthVideo{}
	if len(spin) > 0 {
		sv.spin = spin[0]
	}
	rend := &render.Renderer{Video: sv}
	v := viewer.New(s, rend)
	defer v.Close()
	v.SetQuality(quality)
	w := test.NewTempWindow(t, container.NewBorder(nil, viewer.Transport(s), nil, nil, v))
	w.Resize(fyne.NewSize(winW, winH))
	q.Settle(80*time.Millisecond, 2*time.Second)

	var res uires
	rw, rh := v.RenderSize()
	res.paintWidgetW, res.paintWidgetH = rw, rh
	old := image.NewNRGBA(image.Rect(0, 0, min(rw, 1280), min(rh, 720))) // the old 720p play cap

	stop := kit.DrivePlayback(s)
	defer stop()
	settle()
	done := make(chan struct{})
	var pwg sync.WaitGroup
	pwg.Add(1)
	go func() {
		defer pwg.Done()
		tk := time.NewTicker(4 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-done:
				return
			case <-tk.C:
				t0 := time.Now()
				kit.Do(func() { res.qlat.add(time.Since(t0)) })
			}
		}
	}()

	s.Seek(0)
	s.SetPlaying(true)
	start := time.Now()
	var busy time.Duration
	var last *image.RGBA
	shown := 0
	warm := time.Now().Add(time.Second) // settle-in excluded from the latency numbers
	for time.Since(start) < dur {
		a := time.Now()
		if n := q.Drain(); n > 0 {
			if img := v.ImageRGBA(); img != last {
				last = img
				shown++
				res.renderW, res.renderH = img.Rect.Dx(), img.Rect.Dy()
			}
			d := time.Since(a)
			busy += d
			if a.After(warm) {
				res.batch.add(d)
			}
		}
		time.Sleep(300 * time.Microsecond)
	}
	elapsed := time.Since(start)
	close(done)
	pwg.Wait()
	res.qualityChanges = v.QualityChanges()
	res.finalQ = v.Stats().Snapshot(time.Now()).Quality.String()
	s.SetPlaying(false)
	// what fyne would have done per frame with the previous NRGBA hand-off, for the log
	for range 20 {
		a := time.Now()
		paintEmu(old, rw, rh)
		res.oldPaint.add(time.Since(a))
	}
	res.shown = float64(shown)
	res.fps = float64(shown) / elapsed.Seconds()
	res.busy = float64(busy) / float64(elapsed)
	_, _, res.dropped, res.late = v.Stats().Counters()
	q.Settle(20*time.Millisecond, time.Second)
	return res
}

// The RGBA hand-off must be a small fraction of the old NRGBA + CatmullRom cost.
func TestPerfPaintGuard(t *testing.T) {
	perfOn(t)
	const ww, wh = 1500, 844
	nrgba := image.NewNRGBA(image.Rect(0, 0, 1280, 720))
	rgba := image.NewRGBA(image.Rect(0, 0, ww, wh))
	var old, cur stats
	for range 30 {
		a := time.Now()
		paintEmu(nrgba, ww, wh)
		old.add(time.Since(a))
		a = time.Now()
		paintEmu(rgba, ww, wh)
		cur.add(time.Since(a))
	}
	t.Logf("painter emulation per frame: old NRGBA+CatmullRom %s | RGBA %s", old.String(), cur.String())
	if cur.pct(.5) > 0.1*old.pct(.5) {
		t.Fatalf("RGBA hand-off %.3f ms is not < 10%% of the NRGBA path %.3f ms", cur.pct(.5), old.pct(.5))
	}
}

// TestPerfPreviewUIBudget: 1080p three-layer project with grade played in a big window
// (2560x1440) with the painter emulated. UI thread: busy < 10%, queue latency p95 < 8 ms.
func TestPerfPreviewUIBudget(t *testing.T) {
	perfOn(t)
	for _, ql := range []string{viewer.QualityAuto, viewer.QualityFull} {
		var runs []uires
		for range 3 {
			runs = append(runs, runPreviewUI(t, 2560, 1440, synthProject(1920, 1080, model.Rate30, 3), ql, 3*time.Second))
		}
		// median run by UI busy
		med := runs[0]
		for _, a := range runs {
			for _, b := range runs {
				if a.busy < b.busy && a.busy > med.busy {
					med = a
				}
			}
		}
		for i, r := range runs {
			t.Logf("q=%-5s run %d: %.1f fps shown, render %dx%d, UI busy %.2f%%, batches %s, kit.Do latency %s, dropped %d late %d | old path per frame %s (widget %dx%d)",
				ql, i, r.fps, r.renderW, r.renderH, 100*r.busy, r.batch.String(), r.qlat.String(), r.dropped, r.late, r.oldPaint.String(), r.paintWidgetW, r.paintWidgetH)
		}
		sl := slack()
		if med.busy > 0.10*sl {
			t.Errorf("q=%s: UI busy %.1f%% (median run), want < %.0f%%", ql, 100*med.busy, 10*sl)
		}
		if p := med.qlat.pct(.95); p > 8*sl {
			t.Errorf("q=%s: kit.Do latency p95 %.1f ms, want < %.0f ms", ql, p, 8*sl)
		}
		if med.fps < 27/sl {
			t.Errorf("q=%s: %.1f fps shown, want >= %.0f", ql, med.fps, 27/sl)
		}
	}
}

// TestPerfPreviewHeavyAdaptive: a 4K project with three layers and a grade on 4 cores
// cannot render at full size in real time; the quality ladder must settle without
// flapping and the UI thread must stay free.
func TestPerfPreviewHeavyAdaptive(t *testing.T) {
	perfOn(t)
	r := runPreviewUI(t, 2560, 1440, synthProject(3840, 2160, model.Rate30, 3), viewer.QualityAuto, 8*time.Second, 12*time.Millisecond)
	t.Logf("heavy 4K x3 auto: %.1f fps shown, render %dx%d (quality %s), quality changes %d, UI busy %.2f%%, kit.Do latency %s, dropped %d late %d",
		r.fps, r.renderW, r.renderH, r.finalQ, r.qualityChanges, 100*r.busy, r.qlat.String(), r.dropped, r.late)
	if r.qualityChanges > 4 {
		t.Errorf("%d quality changes in 8 s (want <= 4)", r.qualityChanges)
	}
	if r.fps < 24/slack() {
		t.Errorf("%.1f fps shown, want >= 24", r.fps)
	}
	if p := r.qlat.pct(.95); p > 8*slack() {
		t.Errorf("kit.Do latency p95 %.1f ms", p)
	}
}
