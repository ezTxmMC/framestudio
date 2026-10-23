package kit

import (
	"testing"

	"framestudio-proto/src/model"
)

func TestParseFramesRejectsNonsense(t *testing.T) {
	for _, s := range []string{"nan", "NaN", "infinity", "inf", "1e30", "-1", "99999999999999999999f", "9999999999f", "100:00:00:00"} {
		if n, err := ParseFrames(s, model.Rate25, false); err == nil {
			t.Errorf("%q accepted: %d", s, n)
		}
	}
	if n, err := ParseFrames("2", model.Rate25, false); err != nil || n != 50 {
		t.Errorf("2 s: %d %v", n, err)
	}
}
