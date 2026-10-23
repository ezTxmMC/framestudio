package audio

import (
	"testing"

	"framestudio-proto/src/model"
)

// BenchmarkPlanRender mixes a 1024 sample block of three overlapping clips at 29.97 fps:
// the mixer must not allocate per block.
func BenchmarkPlanRender(b *testing.B) {
	p, c := project(200, 100)
	p.Settings.FPS = model.NTSC30
	c.FadeIn = 10
	pl := plan(p)
	n := 1024
	out := make([]float32, 2*n)
	trk := make([][]float32, len(pl.Tracks))
	for i := range trk {
		trk[i] = make([]float32, 2*n)
	}
	pos := FramesToSamples(210, model.NTSC30)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pl.Render(pos, 1, Window{}, out, trk, n)
	}
}
