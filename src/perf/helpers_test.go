// Package perf is the preview performance harness: headless benchmarks and measurement
// tests that show where preview time goes (decode, composite, convert, UI hand-off).
//
// Everything here is skipped unless FS_PERF=1 is set, because it needs test media
// (see genmedia.sh in this directory, default dir /tmp/fsprof, override with FS_PERF_MEDIA)
// and takes a few minutes. Run politely:
//
//	FS_PERF=1 GOMAXPROCS=4 nice -n 19 go test ./src/perf -run 'TestPerf' -v -count=1
//	FS_PERF=1 GOMAXPROCS=4 nice -n 19 go test ./src/perf -run xxx -bench . -benchtime 20x
//
// Tests use the fyne test driver only (never a window) and private XDG dirs.
package perf

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"sort"
	"testing"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
)

func need(tb testing.TB) {
	tb.Helper()
	if os.Getenv("FS_PERF") == "" {
		tb.Skip("set FS_PERF=1 to run the preview performance harness")
	}
	if !media.FFmpegAvailable() {
		tb.Skip("ffmpeg not available")
	}
	tb.Setenv("XDG_CONFIG_HOME", tb.TempDir())
	tb.Setenv("XDG_DATA_HOME", tb.TempDir())
}

func mediaDir() string {
	if d := os.Getenv("FS_PERF_MEDIA"); d != "" {
		return d
	}
	return "/tmp/fsprof"
}

// The test media (genmedia.sh).
const (
	H264_1080p30   = "h264_1080p30.mp4"
	H264_4K30      = "h264_4k30.mp4"
	Intra_1080p30  = "intra_1080p30.mp4"
	H264_1080p5994 = "h264_1080p5994.mp4"
	HEVC_4K30      = "hevc_4k30.mp4"
	H264_4K30HB    = "h264_4k30_hb.mp4"    // noisy 80 Mbit/s, camera-like decode cost
	H264_1080p30HB = "h264_1080p30_hb.mp4" // noisy 30 Mbit/s
)

func probe(tb testing.TB, name string) model.MediaItem {
	tb.Helper()
	path := filepath.Join(mediaDir(), name)
	if _, err := os.Stat(path); err != nil {
		tb.Skipf("test media %s missing (run genmedia.sh)", path)
	}
	m, err := media.Probe(path)
	if err != nil {
		tb.Fatal(err)
	}
	return m
}

func newProject(w, h int, fps model.Rate) *model.Project {
	p := model.NewProject("perf")
	p.Settings.Width, p.Settings.Height, p.Settings.FPS = w, h, fps
	return p
}

// addVideo places m on the video track vi from frame 0 for its whole length.
func addVideo(p *model.Project, vi int, m model.MediaItem) *model.Clip {
	p.Media = append(p.Media, m)
	n := p.Rate().DurationToFrames(m.Duration) - 1
	c := model.NewClip(model.KindVideo, m.Name, 0, n)
	c.MediaID = m.ID
	tr := p.TrackIndices(model.TrackVideo)[vi]
	if err := p.PlaceClip(p.Tracks[tr].ID, c); err != nil {
		panic(err)
	}
	return &p.Tracks[tr].Clips[len(p.Tracks[tr].Clips)-1]
}

func ensureVideoTracks(p *model.Project, n int) {
	for len(p.TrackIndices(model.TrackVideo)) < n {
		p.AddTrack(model.TrackVideo)
	}
}

// scenario1: a single clip.
func scenario1(tb testing.TB, name string, w, h int, fps model.Rate) *model.Project {
	p := newProject(w, h, fps)
	addVideo(p, 0, probe(tb, name))
	return p
}

// scenario3: V1 4K/1080p video, V2 intra 1080p video at 70 % opacity, V3 title, and a
// grade on V1 (exposure/contrast/saturation/vignette).
func scenario3(tb testing.TB, w, h int, grade bool) *model.Project {
	p := newProject(w, h, model.Rate30)
	ensureVideoTracks(p, 3)
	c := addVideo(p, 0, probe(tb, H264_1080p30))
	if grade {
		c.Grade = model.Grade{Exposure: 0.2, Contrast: 0.15, Saturation: 0.2, Temperature: 0.1, Vignette: 0.3}
	}
	c2 := addVideo(p, 1, probe(tb, Intra_1080p30))
	c2.Opacity = 0.7
	if grade {
		c2.Grade = model.Grade{Contrast: 0.1, Tint: 0.1}
	}
	t := model.NewClip(model.KindTitle, "Titel", 0, 200)
	t.Title.Text = "Frame Studio Vorschau"
	vt := p.TrackIndices(model.TrackVideo)[2]
	if err := p.PlaceClip(p.Tracks[vt].ID, t); err != nil {
		panic(err)
	}
	return p
}

// ---- statistics ----

type stats struct{ d []float64 } // milliseconds

func (s *stats) add(d time.Duration) { s.d = append(s.d, float64(d)/1e6) }
func (s *stats) n() int              { return len(s.d) }
func (s *stats) sorted() []float64 {
	c := append([]float64(nil), s.d...)
	sort.Float64s(c)
	return c
}
func (s *stats) pct(p float64) float64 {
	c := s.sorted()
	if len(c) == 0 {
		return 0
	}
	i := int(p * float64(len(c)-1))
	return c[i]
}
func (s *stats) mean() float64 {
	if len(s.d) == 0 {
		return 0
	}
	t := 0.0
	for _, v := range s.d {
		t += v
	}
	return t / float64(len(s.d))
}
func (s *stats) max() float64 { return s.pct(1) }
func (s *stats) over(ms float64) int {
	n := 0
	for _, v := range s.d {
		if v > ms {
			n++
		}
	}
	return n
}
func (s *stats) String() string {
	return fmt.Sprintf("n=%d mean=%.2f p50=%.2f p95=%.2f p99=%.2f max=%.2f ms", s.n(), s.mean(), s.pct(.5), s.pct(.95), s.pct(.99), s.max())
}

// ---- GC / memory sampling via runtime/metrics ----

type gcSample struct {
	hist       *metrics.Float64Histogram
	pauseTotal time.Duration
	pauseMax   time.Duration
	cycles     uint64
	allocBytes uint64
	allocObjs  uint64
	heapLive   uint64
}

var gcNames = []string{
	"/gc/pauses:seconds", "/gc/cycles/total:gc-cycles",
	"/gc/heap/allocs:bytes", "/gc/heap/allocs:objects", "/memory/classes/heap/objects:bytes",
}

func readGC() gcSample {
	ms := make([]metrics.Sample, len(gcNames))
	for i, n := range gcNames {
		ms[i].Name = n
	}
	metrics.Read(ms)
	var g gcSample
	if h := ms[0].Value.Float64Histogram(); h != nil {
		g.hist = &metrics.Float64Histogram{Counts: append([]uint64(nil), h.Counts...), Buckets: h.Buckets}
		for i, c := range h.Counts {
			if c == 0 {
				continue
			}
			lo, hi := h.Buckets[i], h.Buckets[i+1]
			mid := (lo + hi) / 2
			if hi > 1e9 || lo < 0 { // +Inf / -Inf edges
				mid = max(lo, 0)
				if mid == 0 {
					mid = hi
				}
			}
			g.pauseTotal += time.Duration(float64(c) * mid * 1e9)
			if hi > 0 && hi < 1e9 {
				if d := time.Duration(hi * 1e9); d > g.pauseMax {
					g.pauseMax = d
				}
			}
		}
	}
	g.cycles = ms[1].Value.Uint64()
	g.allocBytes = ms[2].Value.Uint64()
	g.allocObjs = ms[3].Value.Uint64()
	g.heapLive = ms[4].Value.Uint64()
	return g
}

func (a gcSample) since(b gcSample) gcSample {
	var mx time.Duration
	if a.hist != nil && b.hist != nil {
		for i, c := range a.hist.Counts {
			if c > b.hist.Counts[i] {
				if hi := a.hist.Buckets[i+1]; hi < 1e9 {
					mx = max(mx, time.Duration(hi*1e9))
				}
			}
		}
	}
	a.pauseMax = mx
	return gcSample{pauseTotal: a.pauseTotal - b.pauseTotal, pauseMax: mx, cycles: a.cycles - b.cycles,
		allocBytes: a.allocBytes - b.allocBytes, allocObjs: a.allocObjs - b.allocObjs, heapLive: a.heapLive}
}

func (g gcSample) String() string {
	return fmt.Sprintf("gc cycles=%d pause_total~%.2fms pause_max<=%.2fms alloc=%.0fMB (%d objs) heap=%.0fMB",
		g.cycles, float64(g.pauseTotal)/1e6, float64(g.pauseMax)/1e6, float64(g.allocBytes)/1e6, g.allocObjs, float64(g.heapLive)/1e6)
}

func settle() {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
}
