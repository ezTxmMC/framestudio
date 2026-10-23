package model

import (
	"testing"
	"time"
)

// Every frame of three hours round-trips through drop-frame display and parsing, labels
// are strictly increasing and the clock follows real time.
func TestDropFrameThreeHours(t *testing.T) {
	for _, r := range []Rate{NTSC30, NTSC60} {
		hours3 := r.DurationToFrames(3 * time.Hour)
		prev := -1
		for f := 0; f <= hours3; f++ {
			tc := Timecode(f, r, true)
			got, err := ParseTimecode(tc, r, true)
			if err != nil || got != f {
				t.Fatalf("%v: frame %d -> %q -> %d, %v", r, f, tc, got, err)
			}
			l := LabelFrame(f, r, true)
			if l <= prev {
				t.Fatalf("%v: label of frame %d not increasing", r, f)
			}
			prev = l
			if b, ok := FrameFromLabel(l, r, true); !ok || b != f {
				t.Fatalf("%v: label %d -> %d %v, want %d", r, l, b, ok, f)
			}
		}
		// the clock stays within a frame or two of real time over the three hours
		if tc := Timecode(hours3, r, true); tc[:8] != "03:00:00" && tc[:8] != "02:59:59" {
			t.Errorf("%v: 3 h of frames show %s", r, tc)
		}
	}
}
