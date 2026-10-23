package media

import (
	"context"
	"path/filepath"
	"testing"

	"framestudio-proto/src/model"
)

func TestParseRate(t *testing.T) {
	for in, want := range map[string]model.Rate{
		"30000/1001": model.NTSC30, "24000/1001": model.NTSC24, "25/1": model.Rate25, "60000/2002": model.NTSC30,
		"0/0": {}, "": {}, "abc": {}, "30": model.Rate30, "29.97": model.NTSC30, "1/0": {},
	} {
		if got := parseRate(in); got != want {
			t.Errorf("parseRate(%q) = %v, want %v", in, got, want)
		}
	}
}

func rationalVideo(t *testing.T, rate string) model.MediaItem {
	t.Helper()
	needFFmpeg(t)
	path := filepath.Join(t.TempDir(), "v.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc=size=64x36:rate="+rate+":duration=4",
		"-c:v", "libx264", "-qp", "0", "-g", "30", path)
	return mustProbe(t, path)
}

// The probe keeps 30000/1001 exact, and frames fetched at rational project rates are the
// same by random access, by the sequential reader and through the cache.
func TestRationalRatesMatchSequential(t *testing.T) {
	needFFmpeg(t)
	for _, tc := range []struct {
		src  string
		want model.Rate
		out  model.Rate
	}{
		{"30000/1001", model.NTSC30, model.NTSC30},
		{"30000/1001", model.NTSC30, model.NTSC24},
		{"24000/1001", model.NTSC24, model.Rate25},
		{"25", model.Rate25, model.NTSC60},
	} {
		m := rationalVideo(t, tc.src)
		if m.FPS != tc.want {
			t.Fatalf("probe of %s: %v, want %v", tc.src, m.FPS, tc.want)
		}
		r, err := OpenVideo(m, 0, tc.out, 64, 36)
		if err != nil {
			t.Fatal(err)
		}
		seq := readAll(t, r)
		if want := tc.out.DurationToFrames(m.Duration); len(seq) < want-1 || len(seq) > want+1 {
			t.Fatalf("%s->%v: %d sequential frames, want about %d", tc.src, tc.out, len(seq), want)
		}
		c := NewCache()
		for _, n := range []int{1, 7, 31, 32, 33, len(seq) - 1} {
			one, err := decodeOne(context.Background(), m, frameArgs(m.Path, n, tc.out, 64, 36, 1), 64, 36)
			if err != nil || !sameImage(one, seq[n]) {
				t.Errorf("%s->%v: random access frame %d differs from sequential decoding (%v)", tc.src, tc.out, n, err)
			}
			vf, err := c.VideoFrame(m, n, tc.out, 64, 36)
			if err != nil || !sameImage(vf, seq[n]) {
				t.Errorf("%s->%v: VideoFrame(%d) differs (%v)", tc.src, tc.out, n, err)
			}
		}
		c.Close()
	}
}

func TestFrameArgsAreExact(t *testing.T) {
	args := frameArgs("/x.mp4", 3003, model.NTSC30, 64, 36, 1)
	var ss, vf string
	for i, a := range args {
		switch a {
		case "-ss":
			ss = args[i+1]
		case "-vf":
			vf = args[i+1]
		}
	}
	// frame 3003 starts at 100.2001 s; decoding starts 0.5 s earlier
	if ss != "99.700100000" || vf != "fps=30000/1001,trim=start_pts=3003,scale=64:36:flags=bicubic" {
		t.Errorf("-ss %q -vf %q", ss, vf)
	}
	a := stream{id: "a", path: "p", fps: model.NTSC30, w: 1, h: 1}.frameKey(1)
	b := stream{id: "a", path: "p", fps: model.Rate30, w: 1, h: 1}.frameKey(1)
	if a == b {
		t.Error("cache keys must include the exact rate")
	}
}
