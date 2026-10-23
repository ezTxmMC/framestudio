package audio

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// rampReader streams frames of the ramp signal as s16le stereo.
type rampReader struct {
	ctx    context.Context
	i, n   int64
	closed bool
}

func (r *rampReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.i >= r.n {
		return 0, io.EOF
	}
	w := 0
	for ; w+4 <= len(b) && r.i < r.n; w, r.i = w+4, r.i+1 {
		l := int16(r.i % 16384)
		b[w], b[w+1] = byte(l), byte(uint16(l)>>8)
		b[w+2], b[w+3] = 0x00, 0x20 // 8192 = 0.25
	}
	return w, nil
}

func (r *rampReader) Close() error { r.closed = true; return nil }

// fakeMedia decodes every path to frames ramp frames; paths in block never finish.
type fakeMedia struct {
	mu      sync.Mutex
	frames  int64
	size    map[string]int64
	decodes map[string]int
	block   map[string]bool
}

func newFakeMedia(frames int64) *fakeMedia {
	return &fakeMedia{frames: frames, size: map[string]int64{}, decodes: map[string]int{}, block: map[string]bool{}}
}

func (f *fakeMedia) decode(ctx context.Context, path string) (io.ReadCloser, error) {
	f.mu.Lock()
	f.decodes[path]++
	block := f.block[path]
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &rampReader{ctx: ctx, n: f.frames}, nil
}

func (f *fakeMedia) stat(path string) (int64, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if path == "/missing" {
		return 0, time.Time{}, errors.New("missing")
	}
	return f.size[path], time.Unix(1, 0), nil
}

func (f *fakeMedia) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.decodes[path]
}

func TestCacheDecodesOnce(t *testing.T) {
	fm := newFakeMedia(SampleRate + 100)
	c := NewCache(fm.decode, fm.stat, 0)
	defer c.Close()
	a, err := c.Get("/a")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.Get("/a")
	if a != b {
		t.Fatal("same file version must share the PCM")
	}
	<-a.Done()
	if a.Err() != nil || a.Frames() != SampleRate+100 || fm.count("/a") != 1 {
		t.Fatalf("frames %d err %v decodes %d", a.Frames(), a.Err(), fm.count("/a"))
	}
	if l, r, ok := a.Frame(SampleRate + 5); !ok || l != rampValue(SampleRate+5) || r != 0.25 {
		t.Fatalf("frame across the chunk boundary: %v %v %v", l, r, ok)
	}
	if _, _, ok := a.Frame(SampleRate + 100); ok {
		t.Fatal("past the end")
	}
	if c.Used() != 2*chunkFrames*bytesPerFrame {
		t.Fatalf("used %d", c.Used())
	}
	if _, err := c.Get("/missing"); err == nil {
		t.Fatal("missing files fail")
	}
}

func TestCacheKeyChangesWithFile(t *testing.T) {
	fm := newFakeMedia(10)
	c := NewCache(fm.decode, fm.stat, 0)
	defer c.Close()
	a, _ := c.Get("/a")
	fm.mu.Lock()
	fm.size["/a"] = 99 // the file was replaced
	fm.mu.Unlock()
	b, _ := c.Get("/a")
	if a == b || a.Key() == b.Key() {
		t.Fatal("a changed file decodes again")
	}
	if CacheKey("/a", 1, time.Unix(1, 0)) == CacheKey("/a", 1, time.Unix(2, 0)) {
		t.Fatal("mtime is part of the key")
	}
	c.Drop(a.Key())
	if keys := c.Keys(); len(keys) != 1 || keys[0] != b.Key() {
		t.Fatalf("keys after drop %q", keys)
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	fm := newFakeMedia(SampleRate) // one chunk each
	c := NewCache(fm.decode, fm.stat, 2*chunkFrames*bytesPerFrame)
	defer c.Close()
	get := func(p string) *PCM {
		t.Helper()
		pcm, err := c.Get(p)
		if err != nil {
			t.Fatal(err)
		}
		<-pcm.Done()
		return pcm
	}
	get("/a")
	get("/b")
	get("/a") // a is now the most recent
	get("/c") // evicts b
	keys := c.Keys()
	if len(keys) != 2 || keys[0] != CacheKey("/c", 0, time.Unix(1, 0)) || keys[1] != CacheKey("/a", 0, time.Unix(1, 0)) {
		t.Fatalf("keys %q", keys)
	}
	if c.Used() != 2*chunkFrames*bytesPerFrame {
		t.Fatalf("used %d", c.Used())
	}
	big := newFakeMedia(5 * SampleRate)
	c2 := NewCache(big.decode, big.stat, 2*chunkFrames*bytesPerFrame)
	defer c2.Close()
	p, _ := c2.Get("/long")
	<-p.Done()
	if p.Frames() != 2*SampleRate || c2.Used() > 2*chunkFrames*bytesPerFrame {
		t.Fatalf("a file over budget keeps what fits: %d frames, %d bytes", p.Frames(), c2.Used())
	}
}

func TestCacheCloseStopsDecoders(t *testing.T) {
	fm := newFakeMedia(10)
	fm.block["/slow"] = true
	c := NewCache(fm.decode, fm.stat, 0)
	p, _ := c.Get("/slow")
	q, _ := c.Get("/slow2")
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hangs on a running decoder")
	}
	<-p.Done()
	<-q.Done()
	if _, err := c.Get("/a"); err == nil {
		t.Fatal("closed cache")
	}
}

func TestFFmpegDecoder(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	mp4 := filepath.Join(t.TempDir(), "clip.mp4") // video files with sound count
	if out, err := exec.Command(ff, "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=64x36:rate=25:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1:sample_rate=44100", "-shortest", "-c:a", "aac", mp4).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	c := NewCache(nil, nil, 0)
	defer c.Close()
	p, err := c.Get(mp4)
	if err != nil {
		t.Fatal(err)
	}
	<-p.Done()
	if p.Err() != nil || p.Frames() < SampleRate*9/10 || p.Frames() > SampleRate*11/10 {
		t.Fatalf("decoded %d frames, err %v", p.Frames(), p.Err())
	}
	saved := lookPath
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	defer func() { lookPath = saved }()
	if _, err := FFmpegDecoder(context.Background(), mp4); err != ErrNoFFmpeg {
		t.Fatalf("got %v", err)
	}
}
