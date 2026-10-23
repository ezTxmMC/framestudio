package model

import "testing"

var sinkI int
var sinkI64 int64

func BenchmarkRateConversions(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d := NTSC30.FramesToDuration(i & 0xfffff)
		sinkI += NTSC30.DurationToFrames(d)
		sinkI64 += NTSC30.SamplesAt(i&0xfffff, 48000)
		sinkI += ConvertFrames(i&0xfffff, NTSC30, Rate25)
	}
}

func BenchmarkTimecodeDropFrame(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkI += len(Timecode(i&0xfffff, NTSC30, true))
	}
}
