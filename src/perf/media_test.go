package perf

import (
	"math/rand"
	"testing"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
)

type mcase struct {
	name string
	file string
}

var mediaCases = []mcase{
	{"1080p30 h264 longGOP", H264_1080p30},
	{"4K30 h264 longGOP", H264_4K30},
	{"1080p30 intra", Intra_1080p30},
	{"1080p59.94 h264", H264_1080p5994},
	{"4K30 hevc", HEVC_4K30},
	{"1080p30 h264 30Mbit", H264_1080p30HB},
	{"4K30 h264 80Mbit", H264_4K30HB},
}

func fpsOf(m model.MediaItem) model.Rate { return m.FPS.Or(model.Rate30) }

// TestPerfMediaSequential: cold start latency and unpaced sequential throughput of
// Cache.VideoFrame (ffmpeg prefetcher + frame LRU), then the cost of a warm cache hit.
func TestPerfMediaSequential(t *testing.T) {
	need(t)
	for _, tc := range mediaCases {
		m := probe(t, tc.file)
		fps := fpsOf(m)
		for _, sz := range [][2]int{{960, 540}, {1280, 720}, {1920, 1080}} {
			c := media.NewCache()
			t0 := time.Now()
			if _, err := c.VideoFrame(m, 0, fps, sz[0], sz[1]); err != nil {
				t.Fatal(err)
			}
			first := time.Since(t0)
			var st stats
			const N = 60
			t1 := time.Now()
			for n := 1; n < N; n++ {
				a := time.Now()
				if _, err := c.VideoFrame(m, n, fps, sz[0], sz[1]); err != nil {
					t.Fatal(err)
				}
				st.add(time.Since(a))
			}
			el := time.Since(t1)
			var warm stats
			for n := 1; n < N; n++ {
				a := time.Now()
				c.VideoFrame(m, n, fps, sz[0], sz[1])
				warm.add(time.Since(a))
			}
			t.Logf("SEQ %-22s %4dx%-4d first=%6.1fms  seq %.0f fps (%.1f ms/frame call) | %s | warm hit mean=%.3fms max=%.3fms",
				tc.name, sz[0], sz[1], float64(first)/1e6, float64(N-1)/el.Seconds(), st.mean(), st.String(), warm.mean(), warm.max())
			c.Close()
		}
	}
}

// pacedRun emulates the viewer's render worker against the media cache with the playhead
// as master clock: it always asks for the frame the clock says is due and skips frames it
// is too slow for. rate is the shuttle rate; w,h the requested size.
type pacedResult struct {
	due, delivered, skipped int
	late                    stats // delivery time minus due time of the frame (>=0)
	call                    stats
	firstMs                 float64
}

func pacedRun(t *testing.T, c *media.Cache, m model.MediaItem, fps model.Rate, start int, rate float64, w, h int, dur time.Duration) pacedResult {
	var res pacedResult
	t0 := time.Now()
	last := -1 << 30
	lastN := start
	period := time.Second / time.Duration(fps.Nominal())
	for time.Since(t0) < dur {
		el := time.Since(t0)
		n := start + int(rate*el.Seconds()*fps.Float())
		if n < 0 || n >= fps.DurationToFrames(m.Duration)-1 {
			break
		}
		if n == last { // wait for the next frame boundary
			time.Sleep(time.Millisecond)
			continue
		}
		due := t0.Add(time.Duration(float64(n-start) / (rate * fps.Float()) * float64(time.Second)))
		a := time.Now()
		var err error
		if rate == 1 {
			_, err = c.VideoFrame(m, n, fps, w, h)
		} else {
			_, err = c.PlaybackFrame(m, n, fps, w, h, rate)
		}
		if err != nil {
			t.Fatalf("frame %d rate %v: %v", n, rate, err)
		}
		done := time.Now()
		res.call.add(done.Sub(a))
		if res.delivered == 0 {
			res.firstMs = float64(done.Sub(a)) / 1e6
		}
		l := done.Sub(due)
		if l < 0 {
			l = 0
		}
		res.late.add(l)
		res.delivered++
		if abs(n-lastN) > 1 && res.delivered > 1 {
			res.skipped += abs(n-lastN) - 1
		}
		lastN, last = n, n
	}
	_ = period
	res.due = int(dur.Seconds() * fps.Float() * abs64(rate))
	return res
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
func abs64(a float64) float64 {
	if a < 0 {
		return -a
	}
	return a
}

// TestPerfMediaPaced: 3 s of real-time 1x playback from a cold cache with a wall-clock master.
func TestPerfMediaPaced(t *testing.T) {
	need(t)
	for _, tc := range mediaCases {
		m := probe(t, tc.file)
		fps := fpsOf(m)
		for _, sz := range [][2]int{{1280, 720}, {1920, 1080}} {
			c := media.NewCache()
			r := pacedRun(t, c, m, fps, 0, 1, sz[0], sz[1], 3*time.Second)
			t.Logf("PACED1x %-22s %4dx%-4d first=%.0fms delivered=%d of ~%d (skipped %d) lateness: p50=%.1f p95=%.1f max=%.1f ms; lateFrames(>1 period)=%d",
				tc.name, sz[0], sz[1], r.firstMs, r.delivered, r.due, r.skipped, r.late.pct(.5), r.late.pct(.95), r.late.max(),
				r.late.over(1000/fps.Float()))
			c.Close()
		}
	}
}

// TestPerfScrub: dragging the playhead over a long-GOP clip. random = jump anywhere,
// forward/backward drag = ~3 frames per mouse event. Time to frame per request.
func TestPerfScrub(t *testing.T) {
	need(t)
	for _, tc := range []mcase{mediaCases[1], mediaCases[0], mediaCases[2], mediaCases[4]} {
		m := probe(t, tc.file)
		fps := fpsOf(m)
		total := fps.DurationToFrames(m.Duration) - 2
		for _, sz := range [][2]int{{1280, 720}, {1920, 1080}} {
			c := media.NewCache()
			rnd := rand.New(rand.NewSource(7))
			var rs, fw, bw stats
			for i := 0; i < 24; i++ {
				a := time.Now()
				if _, err := c.VideoFrame(m, rnd.Intn(total), fps, sz[0], sz[1]); err != nil {
					t.Fatal(err)
				}
				rs.add(time.Since(a))
			}
			c.Close()
			c = media.NewCache()
			start := total / 4
			for i := 0; i < 40; i++ {
				a := time.Now()
				if _, err := c.VideoFrame(m, start+i*3, fps, sz[0], sz[1]); err != nil {
					t.Fatal(err)
				}
				fw.add(time.Since(a))
			}
			c.Close()
			c = media.NewCache()
			start = total * 3 / 4
			for i := 0; i < 40; i++ {
				a := time.Now()
				if _, err := c.VideoFrame(m, start-i*3, fps, sz[0], sz[1]); err != nil {
					t.Fatal(err)
				}
				bw.add(time.Since(a))
			}
			c.Close()
			t.Logf("SCRUB %-22s %4dx%-4d random: %s\n      drag fwd(3f/step): %s\n      drag back(3f/step): %s",
				tc.name, sz[0], sz[1], rs.String(), fw.String(), bw.String())
		}
	}
}

// TestPerfShuttle: J/K/L style playback at 2x, 4x, 8x and reverse -1x, -2x, -8x at the
// shuttle preview size, wall-clock master.
func TestPerfShuttle(t *testing.T) {
	need(t)
	for _, tc := range []mcase{mediaCases[0], mediaCases[1], mediaCases[2]} {
		m := probe(t, tc.file)
		fps := fpsOf(m)
		total := fps.DurationToFrames(m.Duration) - 2
		for _, rate := range []float64{2, 4, 8, -1, -2, -4, -8} {
			sz := [2]int{854, 480}
			if rate == -1 {
				sz = [2]int{1280, 720}
			}
			c := media.NewCache()
			start := 0
			if rate < 0 {
				start = total - 5
			}
			r := pacedRun(t, c, m, fps, start, rate, sz[0], sz[1], 1500*time.Millisecond)
			t.Logf("SHUTTLE %-22s %+gx %dx%d first=%.0fms delivered=%d of ~%d due; call: %s; lateness p95=%.0f max=%.0f ms; late>33ms=%d",
				tc.name, rate, sz[0], sz[1], r.firstMs, r.delivered, r.due, r.call.String(), r.late.pct(.95), r.late.max(), r.late.over(33.3))
			c.Close()
		}
	}
}
