package timeline

import (
	"strings"
	"testing"

	"framestudio-proto/src/model"
)

// Major ticks sit on whole timecode seconds and minutes at every rate; in drop-frame the
// minute ticks stay (on ;02 or ;04, on ;00 in every tenth minute) and frames increase.
func TestRulerTicksLandOnWholeTimecodeUnits(t *testing.T) {
	for _, tc := range []struct {
		rate model.Rate
		drop bool
		ppf  float64
		end  string // suffix of every major label
	}{
		{model.NTSC24, false, 8, ":00"},
		{model.NTSC30, false, 8, ":00"},
		{model.NTSC30, true, 8, ";00"},
		{model.NTSC60, true, 4, ";00"},
	} {
		ticks := rulerTicks(tc.rate, tc.drop, tc.ppf, 0, 3000)
		majors, prev := 0, -1
		for _, tk := range ticks {
			if tk.frame <= prev {
				t.Fatalf("%v: ticks not increasing at frame %d", tc.rate, tk.frame)
			}
			prev = tk.frame
			if tk.major {
				majors++
				if lbl := model.Timecode(tk.frame, tc.rate, tc.drop); !strings.HasSuffix(lbl, tc.end) && !(tc.drop && (strings.HasSuffix(lbl, ";02") || strings.HasSuffix(lbl, ";04"))) {
					t.Errorf("%v drop=%v: major tick at frame %d reads %s", tc.rate, tc.drop, tk.frame, lbl)
				}
			}
		}
		if majors < 3 {
			t.Errorf("%v: only %d major ticks", tc.rate, majors)
		}
	}
	// zoomed far out in drop-frame the minute majors exist and read ;02 except every tenth
	n := 0
	for _, tk := range rulerTicks(model.NTSC30, true, 0.02, 0, 60*60*30) {
		if !tk.major {
			continue
		}
		lbl := model.Timecode(tk.frame, model.NTSC30, true)
		if !strings.HasSuffix(lbl, ";00") && !strings.HasSuffix(lbl, ";02") {
			t.Errorf("minute tick reads %s", lbl)
		}
		n++
	}
	if n < 10 {
		t.Errorf("only %d minute ticks in the first hour", n)
	}
}
