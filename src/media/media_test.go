package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"framestudio-proto/src/model"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	if !FFmpegAvailable() {
		t.Skip("ffmpeg nicht installiert")
	}
}

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("ffmpeg", append([]string{"-nostdin", "-loglevel", "error", "-y"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

// testVideo writes a 2 s 160x90 25 fps clip with a sine tone.
func testVideo(t *testing.T) string {
	t.Helper()
	needFFmpeg(t)
	path := filepath.Join(t.TempDir(), "clip.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc=size=160x90:rate=25:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path)
	return path
}

func testAudio(t *testing.T) string {
	t.Helper()
	needFFmpeg(t)
	path := filepath.Join(t.TempDir(), "tone.wav")
	ffmpeg(t, "-f", "lavfi", "-i", "sine=frequency=220:duration=1.5", path)
	return path
}

func testPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 0x80, 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "Standbild.PNG")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustProbe(t *testing.T, path string) model.MediaItem {
	t.Helper()
	m, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestKindForPath(t *testing.T) {
	cases := map[string]model.ClipKind{"a.PNG": model.KindImage, "b.tiff": model.KindImage,
		"c.MoV": model.KindVideo, "d.webm": model.KindVideo, "e.opus": model.KindAudio, "f.m4a": model.KindAudio}
	for p, want := range cases {
		if k, ok := KindForPath(p); !ok || k != want {
			t.Errorf("%s: %v %v, want %v", p, k, ok, want)
		}
	}
	for _, p := range []string{"x.txt", "noext", "y.fsproj"} {
		if _, ok := KindForPath(p); ok {
			t.Errorf("%s should be unsupported", p)
		}
	}
	if len(Extensions()) != len(extKinds) {
		t.Errorf("Extensions lists %d of %d", len(Extensions()), len(extKinds))
	}
}

func TestProbeImage(t *testing.T) {
	m := mustProbe(t, testPNG(t, 64, 48))
	if m.Kind != model.KindImage || m.Width != 64 || m.Height != 48 || m.Name != "Standbild" || m.ID == "" {
		t.Fatalf("unexpected %+v", m)
	}
	if !filepath.IsAbs(m.Path) || m.Duration != 0 {
		t.Fatalf("unexpected %+v", m)
	}
}

func TestProbeErrors(t *testing.T) {
	if _, err := Probe(filepath.Join(t.TempDir(), "fehlt.mp4")); err == nil {
		t.Error("missing file should fail")
	}
	bad := filepath.Join(t.TempDir(), "kaputt.png")
	_ = os.WriteFile(bad, []byte("kein Bild"), 0o644)
	if _, err := Probe(bad); err == nil {
		t.Error("corrupt image should fail")
	}
	txt := filepath.Join(t.TempDir(), "notiz.txt")
	_ = os.WriteFile(txt, []byte("x"), 0o644)
	if _, err := Probe(txt); err == nil {
		t.Error("unsupported file should fail")
	}
}

func TestApplyProbe(t *testing.T) {
	js := `{"streams":[{"codec_type":"video","width":1920,"height":1080,"r_frame_rate":"30000/1001",
	"avg_frame_rate":"30000/1001","side_data_list":[{"rotation":-90}]},{"codec_type":"audio"}],
	"format":{"duration":"12.5"}}`
	m := model.MediaItem{Kind: model.KindVideo}
	if err := applyProbe(&m, []byte(js)); err != nil {
		t.Fatal(err)
	}
	if m.Width != 1080 || m.Height != 1920 || m.FPS != 29.97 || !m.HasAudio || m.Duration != 12500*time.Millisecond {
		t.Fatalf("unexpected %+v", m)
	}
	m = model.MediaItem{Kind: model.KindVideo}
	if err := applyProbe(&m, []byte(`{"streams":[{"codec_type":"audio"}],"format":{"duration":"3"}}`)); err != nil || m.Kind != model.KindAudio {
		t.Fatalf("audio-only mp4: %+v %v", m, err)
	}
	m = model.MediaItem{Kind: model.KindVideo}
	if err := applyProbe(&m, []byte(`{"streams":[],"format":{}}`)); err == nil {
		t.Fatal("empty container should fail")
	}
}

func TestProbeVideoAndAudio(t *testing.T) {
	m := mustProbe(t, testVideo(t))
	if m.Kind != model.KindVideo || m.Width != 160 || m.Height != 90 || m.FPS != 25 || !m.HasAudio || m.Name != "clip" {
		t.Fatalf("unexpected %+v", m)
	}
	if m.Duration < 1900*time.Millisecond || m.Duration > 2200*time.Millisecond {
		t.Fatalf("duration %v", m.Duration)
	}
	a := mustProbe(t, testAudio(t))
	if a.Kind != model.KindAudio || !a.HasAudio || a.Width != 0 || a.Duration < 1400*time.Millisecond {
		t.Fatalf("unexpected %+v", a)
	}
}

func TestFitSize(t *testing.T) {
	cases := [][6]int{
		{1920, 1080, 320, 320, 320, 180},
		{1080, 1920, 320, 180, 101, 180},
		{100, 100, 50, 80, 50, 50},
		{160, 90, 640, 360, 640, 360},
	}
	for _, c := range cases {
		if w, h := fitSize(c[0], c[1], c[2], c[3]); w != c[4] || h != c[5] {
			t.Errorf("fit %v: got %dx%d", c[:4], w, h)
		}
	}
}

func TestImageScalingAndCache(t *testing.T) {
	c := NewCache()
	m := mustProbe(t, testPNG(t, 200, 100))
	img, err := c.Image(m, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 50 {
		t.Fatalf("scaled to %v", b)
	}
	again, _ := c.Image(m, 100, 100)
	if again != img {
		t.Fatal("second request should hit the cache")
	}
	full, _ := c.Image(m, 400, 100)
	if b := full.Bounds(); b.Dx() != 200 || b.Dy() != 100 {
		t.Fatalf("exact fit should return full image, got %v", b)
	}
	c.Invalidate(m.ID)
	if c.stills.len() != 0 {
		t.Fatalf("invalidate left %d entries", c.stills.len())
	}
	if th, err := c.Thumbnail(m, 50, 50); err != nil || th.Bounds().Dx() != 50 {
		t.Fatalf("thumbnail %v %v", th, err)
	}
	m.Path = filepath.Join(t.TempDir(), "weg.png")
	if _, err := c.Image(m, 10, 10); err == nil {
		t.Fatal("missing still should fail")
	}
}

func TestLRUEviction(t *testing.T) {
	l := newLRU(10)
	l.put("a", "x", 1, 4)
	l.put("b", "y", 2, 4)
	l.get("a")
	l.put("c", "x", 3, 4) // evicts b, the least recently used
	if _, ok := l.get("b"); ok {
		t.Fatal("b should be evicted")
	}
	if _, ok := l.get("a"); !ok {
		t.Fatal("a should survive")
	}
	l.put("huge", "z", 4, 11)
	if _, ok := l.get("huge"); ok {
		t.Fatal("entries over budget must not be stored")
	}
	l.dropOwner("x")
	if l.len() != 0 || l.used != 0 {
		t.Fatalf("dropOwner left %d entries, %d bytes", l.len(), l.used)
	}
}

func sameImage(a, b image.Image) bool {
	ra, ok1 := a.(*image.RGBA)
	rb, ok2 := b.(*image.RGBA)
	return ok1 && ok2 && ra.Rect == rb.Rect && bytes.Equal(ra.Pix, rb.Pix)
}

func TestVideoFrame(t *testing.T) {
	m := mustProbe(t, testVideo(t))
	c := NewCache()
	defer c.Close()
	f0, err := c.VideoFrame(m, 0, 25, 80, 80)
	if err != nil {
		t.Fatal(err)
	}
	if b := f0.Bounds(); b.Dx() != 80 || b.Dy() != 45 {
		t.Fatalf("frame size %v", b)
	}
	f10, err := c.VideoFrame(m, 10, 25, 80, 80)
	if err != nil {
		t.Fatal(err)
	}
	if sameImage(f0, f10) {
		t.Fatal("frames 0 and 10 should differ")
	}
	// The prefetcher decodes ahead while the viewer shows a frame, so the
	// following frames are cache hits.
	time.Sleep(300 * time.Millisecond)
	hits := c.hits.Load()
	for n := 11; n < 20; n++ {
		if _, err := c.VideoFrame(m, n, 25, 80, 80); err != nil {
			t.Fatal(err)
		}
	}
	if c.hits.Load()-hits < 5 {
		t.Fatalf("sequential frames should mostly hit the cache, got %d hits", c.hits.Load()-hits)
	}
	// Prefetched frames match direct single-frame decodes.
	one, err := decodeOne(context.Background(), m, frameArgs(m.Path, 15, 25, 80, 45, 1), 80, 45)
	if err != nil {
		t.Fatal(err)
	}
	f15, _ := c.VideoFrame(m, 15, 25, 80, 80)
	if !sameImage(one, f15) {
		t.Error("prefetched frame 15 differs from a direct decode")
	}
	// Past the end the last frame is held.
	if _, err := c.VideoFrame(m, 500, 25, 80, 80); err != nil {
		t.Fatal(err)
	}
	c.Invalidate(m.ID)
	if c.frames.len() != 0 {
		t.Fatal("invalidate should drop frames")
	}
}

func TestTwoPlayheadsShareStream(t *testing.T) {
	needFFmpeg(t)
	path := filepath.Join(t.TempDir(), "lang.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc=size=64x36:rate=25:duration=6", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	m := mustProbe(t, path)
	c := NewCache()
	defer c.Close()
	// Two clips of the same file, 4 s apart, play in parallel.
	for n := 0; n < 6; n++ {
		for _, base := range []int{0, 100} {
			if _, err := c.VideoFrame(m, base+n, 25, 64, 36); err != nil {
				t.Fatal(err)
			}
		}
	}
	c.pmu.Lock()
	n := len(c.prefetchers)
	c.pmu.Unlock()
	if n != 2 {
		t.Fatalf("want one decoder per playing clip, got %d", n)
	}
}

func TestPrefetcherLimit(t *testing.T) {
	m := mustProbe(t, testVideo(t))
	c := NewCache()
	for i := 0; i < maxPrefetchers+3; i++ {
		if _, err := c.VideoFrame(m, i*7%50, 25, 40+i*8, 40+i*8); err != nil {
			t.Fatal(err)
		}
	}
	c.pmu.Lock()
	n := len(c.prefetchers)
	c.pmu.Unlock()
	if n > maxPrefetchers {
		t.Fatalf("%d prefetchers running", n)
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the decoders")
	}
}

func TestOpenVideo(t *testing.T) {
	m := mustProbe(t, testVideo(t))
	r, err := OpenVideo(m, 0, 25, 160, 90)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for {
		img, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 160 {
			t.Fatalf("size %v", img.Bounds())
		}
		count++
	}
	_ = r.Close()
	if count != 50 {
		t.Fatalf("got %d frames, want 50", count)
	}
	// Starting at frame 25 at 10 fps (1 s of 2 s) gives 10 frames.
	r, err = OpenVideo(m, 10, 10, 80, 45)
	if err != nil {
		t.Fatal(err)
	}
	count = 0
	for ; ; count++ {
		if _, err := r.Next(); err != nil {
			break
		}
	}
	if count < 9 || count > 11 {
		t.Fatalf("got %d frames from 1 s at 10 fps", count)
	}
	_ = r.Close()
	// Closing early kills the process.
	r, _ = OpenVideo(m, 0, 25, 160, 90)
	_, _ = r.Next()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(); err == nil {
		t.Fatal("Next after Close should fail")
	}
}

func TestWaveform(t *testing.T) {
	c := NewCache()
	a := mustProbe(t, testAudio(t))
	w, err := c.Waveform(a, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != 100 {
		t.Fatalf("got %d buckets", len(w))
	}
	var peak float32
	for _, v := range w {
		if v < 0 || v > 1 {
			t.Fatalf("peak %v out of range", v)
		}
		peak = max(peak, v)
	}
	if peak < 0.05 {
		t.Fatalf("sine should not be silent, peak %v", peak)
	}
	if c.waves.len() != 1 {
		t.Fatal("envelope should be cached")
	}
	if w2, _ := c.Waveform(a, 7); len(w2) != 7 {
		t.Fatal("resampling from cache failed")
	}
	th, err := c.Thumbnail(a, 120, 60)
	if err != nil || th.Bounds().Dx() != 120 {
		t.Fatalf("audio thumbnail %v %v", th, err)
	}
	v := mustProbe(t, testVideo(t))
	if w, err := c.Waveform(v, 10); err != nil || len(w) != 10 {
		t.Fatalf("video sound track: %v", err)
	}
	if th, err := c.Thumbnail(v, 64, 64); err != nil || th.Bounds().Dx() != 64 || th.Bounds().Dy() != 36 {
		t.Fatalf("video thumbnail %v %v", th, err)
	}
}

func TestEnvelopeAndResample(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < samplesPerBin*3; i++ {
		v := int16(0)
		if i == samplesPerBin+3 {
			v = -16384
		}
		_ = binary.Write(&buf, binary.LittleEndian, v)
	}
	p, err := envelope(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 3 || p[0] != 0 || p[1] < 0.49 || p[1] > 0.51 {
		t.Fatalf("envelope %v", p)
	}
	if r := resamplePeaks([]float32{0, 1, 0, 0}, 2); r[0] != 1 || r[1] != 0 {
		t.Fatalf("downsample %v", r)
	}
	if r := resamplePeaks([]float32{0.5}, 3); r[2] != 0.5 {
		t.Fatalf("upsample %v", r)
	}
}

// rateVideo writes a 64x36 test pattern with every frame distinct.
func rateVideo(t *testing.T, rate, seconds int) model.MediaItem {
	t.Helper()
	needFFmpeg(t)
	path := filepath.Join(t.TempDir(), fmt.Sprintf("v%d.mp4", rate))
	ffmpeg(t, "-f", "lavfi", "-i", fmt.Sprintf("testsrc=size=64x36:rate=%d:duration=%d", rate, seconds),
		"-c:v", "libx264", "-qp", "0", "-g", strconv.Itoa(rate), path)
	return mustProbe(t, path)
}

func readAll(t *testing.T, r FrameReader) []image.Image {
	t.Helper()
	defer r.Close()
	var out []image.Image
	for {
		img, err := r.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, img)
	}
}

func TestRandomAccessMatchesSequential(t *testing.T) {
	for _, tc := range []struct{ src, fps int }{{30, 25}, {25, 30}, {25, 25}} {
		m := rateVideo(t, tc.src, 6)
		r, err := OpenVideo(m, 0, tc.fps, 64, 36)
		if err != nil {
			t.Fatal(err)
		}
		seq := readAll(t, r)
		if len(seq) != 6*tc.fps {
			t.Fatalf("%d->%d: %d sequential frames, want %d", tc.src, tc.fps, len(seq), 6*tc.fps)
		}
		c := NewCache()
		for _, n := range []int{1, 5, 7, 12, 37, 61, 99, 6*tc.fps - 1} {
			ctx := context.Background()
			one, err := decodeOne(ctx, m, frameArgs(m.Path, n, tc.fps, 64, 36, 1), 64, 36)
			if err != nil {
				t.Fatalf("%d->%d frame %d: %v", tc.src, tc.fps, n, err)
			}
			if !sameImage(one, seq[n]) {
				t.Errorf("%d->%d: random access frame %d differs from sequential decoding", tc.src, tc.fps, n)
			}
			r, err := OpenVideo(m, n, tc.fps, 64, 36)
			if err != nil {
				t.Fatal(err)
			}
			first, err := r.Next()
			r.Close()
			if err != nil || !sameImage(first, seq[n]) {
				t.Errorf("%d->%d: OpenVideo(%d) differs from sequential decoding (%v)", tc.src, tc.fps, n, err)
			}
			vf, err := c.VideoFrame(m, n, tc.fps, 64, 36)
			if err != nil || !sameImage(vf, seq[n]) {
				t.Errorf("%d->%d: VideoFrame(%d) differs from sequential decoding (%v)", tc.src, tc.fps, n, err)
			}
		}
		c.Close()
	}
}

func TestProbeUsesVideoStreamDuration(t *testing.T) {
	needFFmpeg(t)
	path := filepath.Join(t.TempDir(), "longaud.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=64x36:rate=25:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=5", "-c:v", "libx264", "-c:a", "aac", path)
	m := mustProbe(t, path)
	if d := m.Duration.Seconds(); d < 3.95 || d > 4.05 {
		t.Fatalf("duration %.3f s, want the 4 s of picture", d)
	}
	c := NewCache()
	defer c.Close()
	if _, err := c.VideoFrame(m, 99, 25, 64, 36); err != nil {
		t.Fatalf("last frame: %v", err)
	}
}

func TestCancelStopsBackgroundJobs(t *testing.T) {
	c := NewCache()
	defer c.Close()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := c.background("m", time.Minute, func(ctx context.Context) (any, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		done <- err
	}()
	<-started
	c.Cancel("m")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled job should fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Cancel did not stop the job")
	}
}

func TestBackgroundJobsAreLimited(t *testing.T) {
	c := NewCache()
	defer c.Close()
	var running, peak atomic.Int32
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.background(fmt.Sprint(i), time.Minute, func(context.Context) (any, error) {
				n := running.Add(1)
				for {
					p := peak.Load()
					if n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				running.Add(-1)
				return nil, nil
			})
		}()
	}
	wg.Wait()
	if p := peak.Load(); p > maxBackground {
		t.Fatalf("%d background jobs ran at once, limit %d", p, maxBackground)
	}
}
