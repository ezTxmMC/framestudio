package kit

import "testing"

func TestParseFrames(t *testing.T) {
	cases := map[string]int{"2": 50, "1,5": 38, "48f": 48, "00:00:02:12": 62, "01:00": 25, "1:00:00": 1500}
	for in, want := range cases {
		if got, err := ParseFrames(in, 25); err != nil || got != want {
			t.Errorf("%q: got %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "x", "00:00:00:30", "-1"} {
		if _, err := ParseFrames(bad, 25); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}
