// Package audio is Frame Studio's live audio engine: a shared PCM cache of decoded
// media, an in-process mixer that follows export's mixdown semantics, and one
// persistent output process that the mix is streamed into in small blocks.
package audio

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// SampleRate and Channels describe all PCM in the engine (s16, interleaved).
	SampleRate = 48000
	Channels   = 2
	// bytesPerFrame is one stereo s16 sample frame.
	bytesPerFrame = 2 * Channels
	// chunkFrames is the allocation unit of decoded PCM (one second).
	chunkFrames = SampleRate
	// DefaultBudget is the memory the PCM cache may hold (~1.5 GB, about 2 h of audio).
	DefaultBudget = 1536 << 20
	// decoders is how many files decode at the same time.
	decoders = 2
)

// ErrNoFFmpeg is reported when media audio cannot be decoded because ffmpeg is missing.
var ErrNoFFmpeg = errors.New("Wiedergabe ohne Ton: ffmpeg wurde nicht gefunden. ffmpeg installieren, um beim Abspielen zu hören.")

// Source is decoded audio addressable by sample frame.
type Source interface {
	// Frame returns sample frame i as -1..1 floats; ok is false outside the decoded range.
	Frame(i int64) (l, r float32, ok bool)
}

type chunk [chunkFrames * Channels]int16

// PCM is the decoded audio of one media file at 48 kHz stereo. It fills progressively
// while the decoder runs; readers see every frame below Frames() without locking.
type PCM struct {
	key    string
	path   string
	chunks atomic.Pointer[[]*chunk]
	frames atomic.Int64
	done   chan struct{}
	err    error // set before done closes
	cancel context.CancelFunc
}

func newPCM(key, path string) *PCM {
	p := &PCM{key: key, path: path, done: make(chan struct{})}
	p.chunks.Store(&[]*chunk{})
	return p
}

// Key identifies the decoded file version (path, size and modification time).
func (p *PCM) Key() string { return p.key }

// Frames is the number of sample frames decoded so far.
func (p *PCM) Frames() int64 { return p.frames.Load() }

// Done is closed when decoding finished, failed or was cancelled.
func (p *PCM) Done() <-chan struct{} { return p.done }

// Err is the decode error after Done (nil while running or on success).
func (p *PCM) Err() error {
	select {
	case <-p.done:
		return p.err
	default:
		return nil
	}
}

// Frame implements Source.
func (p *PCM) Frame(i int64) (l, r float32, ok bool) {
	if i < 0 || i >= p.frames.Load() {
		return 0, 0, false
	}
	cs := *p.chunks.Load()
	c := cs[i/chunkFrames]
	j := (i % chunkFrames) * Channels
	return float32(c[j]) * (1.0 / 32768), float32(c[j+1]) * (1.0 / 32768), true
}

// Bytes is the memory the decoded samples occupy.
func (p *PCM) Bytes() int64 { return int64(len(*p.chunks.Load())) * chunkFrames * bytesPerFrame }

// Decoder opens a stream of s16le stereo 48 kHz PCM for a media file.
type Decoder func(ctx context.Context, path string) (io.ReadCloser, error)

// Stat returns the identity of a file version; tests replace it.
type Stat func(path string) (size int64, mtime time.Time, err error)

func osStat(path string) (int64, time.Time, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, time.Time{}, err
	}
	if st.IsDir() {
		return 0, time.Time{}, fmt.Errorf("%s ist ein Ordner", path)
	}
	return st.Size(), st.ModTime(), nil
}

// CacheKey builds the cache key of a file version.
func CacheKey(path string, size int64, mtime time.Time) string {
	return path + "\x00" + strconv.FormatInt(size, 10) + "\x00" + strconv.FormatInt(mtime.UnixNano(), 10)
}

// Cache decodes each media file once and keeps the PCM in memory, least recently
// used first out when the budget is exceeded. Safe for concurrent use.
type Cache struct {
	decode Decoder
	stat   Stat
	budget int64

	mu    sync.Mutex
	used  int64
	ll    *list.List // of *PCM, front = most recent
	items map[string]*list.Element

	sem    chan struct{}
	ctx    context.Context
	stop   context.CancelFunc
	wg     sync.WaitGroup
	closed bool
}

// NewCache returns a cache with the given decoder (nil = ffmpeg), stat (nil = os.Stat)
// and budget in bytes (<= 0 = DefaultBudget).
func NewCache(decode Decoder, stat Stat, budget int64) *Cache {
	if decode == nil {
		decode = FFmpegDecoder
	}
	if stat == nil {
		stat = osStat
	}
	if budget <= 0 {
		budget = DefaultBudget
	}
	ctx, stop := context.WithCancel(context.Background())
	return &Cache{decode: decode, stat: stat, budget: budget, ll: list.New(), items: map[string]*list.Element{},
		sem: make(chan struct{}, decoders), ctx: ctx, stop: stop}
}

// Get returns the PCM of the file at path, starting a background decode on first use.
// It fails if the file cannot be read (missing, offline).
func (c *Cache) Get(path string) (*PCM, error) {
	size, mtime, err := c.stat(path)
	if err != nil {
		return nil, err
	}
	key := CacheKey(path, size, mtime)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, context.Canceled
	}
	if e, ok := c.items[key]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*PCM), nil
	}
	p := newPCM(key, path)
	ctx, cancel := context.WithCancel(c.ctx)
	p.cancel = cancel
	c.items[key] = c.ll.PushFront(p)
	c.wg.Add(1)
	go c.run(ctx, p)
	return p, nil
}

// Drop forgets the PCM stored under key and stops its decoder (e.g. after a relink).
func (c *Cache) Drop(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.remove(e)
	}
}

// Used is the number of bytes held.
func (c *Cache) Used() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

// Keys lists the cached keys, most recently used first.
func (c *Cache) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var keys []string
	for e := c.ll.Front(); e != nil; e = e.Next() {
		keys = append(keys, e.Value.(*PCM).key)
	}
	return keys
}

// Close stops all decoders, waits for them and empties the cache.
func (c *Cache) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.stop()
	c.wg.Wait()
	c.mu.Lock()
	for e := c.ll.Front(); e != nil; e = c.ll.Front() {
		c.remove(e)
	}
	c.mu.Unlock()
}

// remove unlinks e; c.mu must be held.
func (c *Cache) remove(e *list.Element) {
	p := e.Value.(*PCM)
	c.ll.Remove(e)
	delete(c.items, p.key)
	c.used -= p.Bytes()
	p.cancel()
}

// grow adds a chunk to p, evicting older entries. It returns nil when p was dropped
// or alone exceeds the budget, which ends its decode.
func (c *Cache) grow(p *PCM) []*chunk {
	const n = chunkFrames * bytesPerFrame
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[p.key]
	if !ok || e.Value.(*PCM) != p {
		return nil
	}
	if p.Bytes()+n > c.budget {
		return nil
	}
	cs := *p.chunks.Load()
	next := append(append(make([]*chunk, 0, len(cs)+1), cs...), new(chunk))
	p.chunks.Store(&next)
	c.used += n
	for c.used > c.budget {
		back := c.ll.Back()
		if back == e {
			back = back.Prev()
		}
		if back == nil {
			break
		}
		c.remove(back)
	}
	return next
}

// run decodes p, publishing frames as they arrive.
func (c *Cache) run(ctx context.Context, p *PCM) {
	defer c.wg.Done()
	defer close(p.done)
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		p.err = ctx.Err()
		return
	}
	r, err := c.decode(ctx, p.path)
	if err != nil {
		p.err = err
		return
	}
	defer r.Close()
	p.err = c.fill(ctx, p, r)
}

// fill copies s16le stereo from r into p's chunks.
func (c *Cache) fill(ctx context.Context, p *PCM, r io.Reader) error {
	buf := make([]byte, 64<<10)
	have := 0
	var frames int64
	for {
		n, err := r.Read(buf[have:])
		have += n
		whole := have - have%bytesPerFrame
		for off := 0; off < whole; {
			cs := *p.chunks.Load()
			ci := int(frames / chunkFrames)
			if ci == len(cs) {
				if cs = c.grow(p); cs == nil {
					return ctx.Err() // dropped or over budget: keep what we have
				}
			}
			ch := cs[ci]
			j := int(frames%chunkFrames) * Channels
			for ; off < whole && j < len(ch); off, j = off+2, j+1 {
				ch[j] = int16(uint16(buf[off]) | uint16(buf[off+1])<<8)
			}
			frames = int64(ci)*chunkFrames + int64(j/Channels)
			p.frames.Store(frames)
		}
		have = copy(buf, buf[whole:have])
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
}

var lookPath = exec.LookPath

// FFmpegDecoder decodes the first audio stream of path with ffmpeg.
func FFmpegDecoder(ctx context.Context, path string) (io.ReadCloser, error) {
	ff, err := lookPath("ffmpeg")
	if err != nil {
		return nil, ErrNoFFmpeg
	}
	cmd := exec.CommandContext(ctx, ff, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", path, "-map", "0:a:0", "-vn", "-ac", strconv.Itoa(Channels), "-ar", strconv.Itoa(SampleRate),
		"-f", "s16le", "-")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg lässt sich nicht starten (%v)", err)
	}
	return &cmdReader{ReadCloser: out, cmd: cmd}, nil
}

// cmdReader waits for the process when the stream is closed.
type cmdReader struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (r *cmdReader) Close() error {
	err := r.ReadCloser.Close()
	_ = r.cmd.Process.Kill() // a no-op when ffmpeg already finished
	_ = r.cmd.Wait()
	return err
}
