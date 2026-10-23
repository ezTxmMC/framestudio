package media

import (
	"bufio"
	"context"
	"image"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"framestudio-proto/src/model"
	"framestudio-proto/src/workgate"
)

// Keyframe index: the times of all keyframes of a video, read once from the container
// (no decoding) in the background. It gives
//   - exact seeks: decoding starts at the keyframe at or before the wanted frame instead
//     of a fixed half second earlier, which could land one whole GOP too early;
//   - the keyframe ladder, small pictures of the keyframes for instant approximate scrub.

const (
	kfIndexTimeout = 3 * time.Minute
	kfMaxFileBytes = 8 << 30 // bigger files are not indexed: reading every packet takes too long
)

type kfEntry struct {
	once  sync.Once
	times []time.Duration // sorted, relative to the start of the file
	ready chan struct{}
	ok    bool
}

type kfStore struct {
	mu sync.Mutex
	m  map[string]*kfEntry
}

func kfKey(m model.MediaItem) string { return m.ID + "|" + m.Path }

func (s *kfStore) forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.m {
		if strings.HasPrefix(k, id+"|") {
			delete(s.m, k)
		}
	}
}

func (s *kfStore) entry(m model.MediaItem) *kfEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]*kfEntry{}
	}
	e := s.m[kfKey(m)]
	if e == nil {
		e = &kfEntry{ready: make(chan struct{})}
		s.m[kfKey(m)] = e
	}
	return e
}

// KeyframeTimes returns the keyframe index of m, reading it when needed. It blocks (up to
// kfIndexTimeout or ctx) so call it from background code; playback uses the non-blocking
// keyframeSeek.
func (c *Cache) KeyframeTimes(ctx context.Context, m model.MediaItem) ([]time.Duration, bool) {
	e := c.kf.entry(m)
	c.startKFIndex(e, m)
	select {
	case <-e.ready:
		return e.times, e.ok
	case <-ctx.Done():
		return nil, false
	}
}

// startKFIndex loads the index in the background (once per entry).
func (c *Cache) startKFIndex(e *kfEntry, m model.MediaItem) {
	e.once.Do(func() {
		c.pmu.Lock()
		closed := c.closed
		if !closed {
			c.wg.Add(1)
		}
		c.pmu.Unlock()
		if closed {
			close(e.ready)
			return
		}
		go func() {
			defer c.wg.Done()
			defer close(e.ready)
			if st, err := os.Stat(m.Path); err != nil || st.Size() > kfMaxFileBytes {
				return
			}
			rel, err := c.gateOf().Acquire(c.ctx, workgate.PrioBackground)
			if err != nil {
				return
			}
			defer rel()
			ctx, cancel := context.WithTimeout(c.ctx, kfIndexTimeout)
			defer cancel()
			e.times, e.ok = c.readKeyframes(ctx, m)
		}()
	})
}

// readKeyframes lists the keyframe times with ffprobe (packets only, no decoding).
func (c *Cache) readKeyframes(ctx context.Context, m model.MediaItem) ([]time.Duration, bool) {
	lookTools()
	if tools.ffprobe == "" {
		return nil, false
	}
	prio := workgate.PrioBackground
	cmd := c.gateOf().Command(ctx, prio, tools.ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time,dts_time,flags:format=start_time", "-of", "csv", m.Path)
	stdout, err := cmd.StdoutPipe()
	var done func()
	if err == nil {
		done, err = workgate.StartCmd(c.gateOf(), cmd, prio)
	}
	if err != nil {
		return nil, false
	}
	var times []float64
	start := 0.0
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		f := strings.Split(sc.Text(), ",")
		switch f[0] {
		case "format":
			if len(f) > 1 {
				start, _ = strconv.ParseFloat(f[1], 64)
			}
		case "packet":
			// packet,pts_time,dts_time,flags
			if len(f) < 4 || !strings.HasPrefix(f[3], "K") {
				continue
			}
			t, err := strconv.ParseFloat(f[1], 64)
			if err != nil {
				if t, err = strconv.ParseFloat(f[2], 64); err != nil {
					continue
				}
			}
			times = append(times, t)
		}
	}
	werr := cmd.Wait()
	done()
	if werr != nil || len(times) == 0 {
		return nil, false
	}
	sort.Float64s(times)
	out := make([]time.Duration, len(times))
	for i, t := range times {
		out[i] = time.Duration((t - start) * float64(time.Second))
	}
	return out, true
}

// before returns the latest keyframe at or before t (false if t precedes the first).
func kfBefore(times []time.Duration, t time.Duration) (time.Duration, bool) {
	i := sort.Search(len(times), func(i int) bool { return times[i] > t })
	if i == 0 {
		return 0, false
	}
	return times[i-1], true
}

// keyframeSeek returns the exact input seek time for decoding that starts at frame n, or
// false when the index is not loaded (the caller then uses the default lead). It never
// blocks: the first call starts loading.
func (c *Cache) keyframeSeek(m model.MediaItem, fps model.Rate, n int) (time.Duration, bool) {
	e := c.kf.entry(m)
	select {
	case <-e.ready:
	default:
		c.startKFIndex(e, m)
		return 0, false
	}
	if !e.ok {
		return 0, false
	}
	t := fps.FramesToDuration(n)
	k, ok := kfBefore(e.times, t)
	if !ok {
		return 0, true // before the first keyframe: from the start of the file
	}
	// A microsecond past the keyframe so rounding of the printed time cannot land on the
	// keyframe before it.
	return k + time.Microsecond, true
}

// --- keyframe ladder ---------------------------------------------------------

const (
	ladderW        = 320
	ladderMaxBytes = 16 << 20 // per source
	ladderSources  = 6
)

type ladder struct {
	times []time.Duration
	imgs  []*image.RGBA
}

type ladderStore struct {
	mu    sync.Mutex
	m     map[string]*ladder
	build map[string]bool
	order []string
}

func (s *ladderStore) forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.m {
		if strings.HasPrefix(k, id+"|") {
			delete(s.m, k)
		}
	}
	for k := range s.build {
		if strings.HasPrefix(k, id+"|") {
			delete(s.build, k)
		}
	}
}

func (c *Cache) ladderFor(m model.MediaItem) *ladder {
	c.ladders.mu.Lock()
	defer c.ladders.mu.Unlock()
	return c.ladders.m[kfKey(m)]
}

// ensureLadder builds the keyframe ladder of m in the background once.
func (c *Cache) ensureLadder(m model.MediaItem) {
	if m.Kind != model.KindVideo || m.Width <= 0 || m.Height <= 0 {
		return
	}
	key := kfKey(m)
	c.ladders.mu.Lock()
	if c.ladders.m[key] != nil || c.ladders.build[key] {
		c.ladders.mu.Unlock()
		return
	}
	if c.ladders.build == nil {
		c.ladders.build = map[string]bool{}
	}
	c.ladders.build[key] = true
	c.ladders.mu.Unlock()

	c.pmu.Lock()
	closed := c.closed
	if !closed {
		c.wg.Add(1)
	}
	c.pmu.Unlock()
	if closed {
		return
	}
	go func() {
		defer c.wg.Done()
		l := c.buildLadder(m)
		c.ladders.mu.Lock()
		defer c.ladders.mu.Unlock()
		if c.ladders.build[key] { // not forgotten (invalidated) meanwhile
			delete(c.ladders.build, key)
			if l != nil {
				if c.ladders.m == nil {
					c.ladders.m = map[string]*ladder{}
				}
				c.ladders.m[key] = l
				c.ladders.order = append(c.ladders.order, key)
				for len(c.ladders.order) > ladderSources {
					delete(c.ladders.m, c.ladders.order[0])
					c.ladders.order = c.ladders.order[1:]
				}
			}
		}
	}()
}

func (c *Cache) buildLadder(m model.MediaItem) *ladder {
	ctx, cancel := context.WithTimeout(c.ctx, kfIndexTimeout)
	defer cancel()
	times, ok := c.KeyframeTimes(ctx, m)
	if !ok || len(times) == 0 {
		return nil
	}
	rel, err := c.gateOf().Acquire(ctx, workgate.PrioBackground)
	if err != nil {
		return nil
	}
	defer rel()
	w, h := fitSize(m.Width, m.Height, ladderW, ladderW)
	w, h = max(w&^1, 2), max(h&^1, 2)
	size := w * h * 4
	stride := 1
	if maxN := ladderMaxBytes / size; len(times) > maxN {
		stride = (len(times) + maxN - 1) / maxN
	}
	vf := "scale=" + strconv.Itoa(w) + ":" + strconv.Itoa(h) + ":flags=bilinear"
	if stride > 1 {
		vf = "select='not(mod(n\\," + strconv.Itoa(stride) + "))'," + vf
	}
	bin, err := ffmpegPath()
	if err != nil {
		return nil
	}
	prio := workgate.PrioBackground
	cmd := c.gateOf().Command(ctx, prio, bin, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-threads", strconv.Itoa(bgThreads), "-skip_frame", "nokey", "-i", m.Path, "-map", "0:v:0", "-an", "-sn", "-dn",
		"-vf", vf, "-fps_mode", "passthrough", "-f", "rawvideo", "-pix_fmt", "rgba", "-")
	stdout, err := cmd.StdoutPipe()
	var done func()
	if err == nil {
		done, err = workgate.StartCmd(c.gateOf(), cmd, prio)
	}
	if err != nil {
		return nil
	}
	l := &ladder{}
	r := bufio.NewReaderSize(stdout, 1<<20)
	for i := 0; ; i++ {
		buf := make([]byte, size)
		if _, err := io.ReadFull(r, buf); err != nil {
			break
		}
		l.imgs = append(l.imgs, rgbaFrame(buf, w, h))
	}
	_ = cmd.Wait()
	done()
	// Keyframes decoded must match the index (every stride-th one is kept); otherwise the
	// pictures cannot be assigned to times and the ladder is dropped.
	want := (len(times) + stride - 1) / stride
	if len(l.imgs) != want {
		return nil
	}
	for i := 0; i < len(times); i += stride {
		l.times = append(l.times, times[i])
	}
	return l
}

// at returns the ladder picture at or before t (the first one if t precedes it).
func (l *ladder) at(t time.Duration) *image.RGBA {
	if l == nil || len(l.imgs) == 0 {
		return nil
	}
	i := sort.Search(len(l.times), func(i int) bool { return l.times[i] > t })
	return l.imgs[max(i-1, 0)]
}
