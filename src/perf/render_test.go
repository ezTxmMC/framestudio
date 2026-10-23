package perf

import (
	"fmt"
	"testing"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
)

type rscen struct {
	name  string
	build func(tb testing.TB) *model.Project
}

func renderScenarios() []rscen {
	return []rscen{
		{"1clip 1080p30", func(tb testing.TB) *model.Project { return scenario1(tb, H264_1080p30, 1920, 1080, model.Rate30) }},
		{"1clip 4K->1080 proj", func(tb testing.TB) *model.Project { return scenario1(tb, H264_4K30, 1920, 1080, model.Rate30) }},
		{"1clip 4K80Mbit->1080 proj", func(tb testing.TB) *model.Project { return scenario1(tb, H264_4K30HB, 1920, 1080, model.Rate30) }},
		{"3 layers+title+grade", func(tb testing.TB) *model.Project { return scenario3(tb, 1920, 1080, true) }},
		{"3 layers+title no grade", func(tb testing.TB) *model.Project { return scenario3(tb, 1920, 1080, false) }},
	}
}

var renderSizes = [][2]int{{960, 540}, {1280, 720}, {1920, 1080}}

// BenchmarkFrameWarm: render.FrameWith with every source frame already in the media
// cache. Measures compositing only: layer pool, RGBA->NRGBA, scale/copy, grade, over,
// title, plus the output frame allocation (a new NRGBA per call).
func BenchmarkFrameWarm(b *testing.B) {
	need(b)
	for _, sc := range renderScenarios() {
		for _, sz := range renderSizes {
			b.Run(fmt.Sprintf("%s/%dx%d", sc.name, sz[0], sz[1]), func(b *testing.B) {
				p := sc.build(b)
				c := media.NewCache()
				defer c.Close()
				r := render.New(c)
				for f := 0; f < 8; f++ { // decode + cache
					r.Frame(p, f, sz[0], sz[1])
				}
				b.SetBytes(int64(sz[0] * sz[1] * 4))
				b.ReportAllocs()
				b.ResetTimer()
				i := 0
				for b.Loop() {
					r.Frame(p, i%8, sz[0], sz[1])
					i++
				}
			})
		}
	}
}

// TestPerfRenderSequential: the full per-frame render cost in sequential playback order
// (decode wait included), unpaced, so ms/frame = the throughput limit of one render worker.
func TestPerfRenderSequential(t *testing.T) {
	need(t)
	for _, sc := range renderScenarios() {
		for _, sz := range renderSizes {
			p := sc.build(t)
			c := media.NewCache()
			r := render.New(c)
			t0 := time.Now()
			r.Frame(p, 0, sz[0], sz[1])
			first := time.Since(t0)
			var st stats
			const N = 90
			g0 := readGC()
			t1 := time.Now()
			for f := 1; f < N; f++ {
				a := time.Now()
				r.Frame(p, f, sz[0], sz[1])
				st.add(time.Since(a))
			}
			el := time.Since(t1)
			g := readGC().since(g0)
			t.Logf("RENDERSEQ %-26s %4dx%-4d first=%6.1fms  %.0f fps; %s\n      %s", sc.name, sz[0], sz[1], float64(first)/1e6,
				float64(N-1)/el.Seconds(), st.String(), g.String())
			c.Close()
		}
	}
}
