package media

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"framestudio-proto/src/model"
)

// The probed duration of a clip of exactly n frames is n frames in a project of the same
// rate, so a clip dragged from the pool is neither a frame short nor a frame long.
func TestProbedDurationIsWholeFrames(t *testing.T) {
	needFFmpeg(t)
	for _, tc := range []struct {
		src  string
		rate model.Rate
	}{{"24000/1001", model.NTSC24}, {"25", model.Rate25}, {"30000/1001", model.NTSC30}, {"60000/1001", model.NTSC60}} {
		for _, n := range []int{1, 73, 120, 301} {
			path := filepath.Join(t.TempDir(), "n.mp4")
			ffmpeg(t, "-f", "lavfi", "-i", "testsrc=size=64x36:rate="+tc.src, "-frames:v", strconv.Itoa(n), "-c:v", "libx264", path)
			m := mustProbe(t, path)
			out, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
				"-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", path).Output()
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(out)); got != strconv.Itoa(n) {
				t.Fatalf("generated %s frames, wanted %d", got, n)
			}
			p := model.NewProject("p")
			p.Settings.FPS = tc.rate
			if got := p.MediaFrames(m); got != n {
				t.Errorf("%s with %d frames: %v (%d ns) is %d project frames", tc.src, n, m.Duration, m.Duration, got)
			}
		}
	}
}
