// Package proxy generates and finds low-resolution all-intra copies of heavy video
// sources. Proxies are used for preview only (never for export): H.264 intra-only at
// 540p decodes in under a millisecond per frame, seeks without walking a GOP and makes
// reverse and fast playback cheap.
//
// Proxies live in the user cache directory, keyed by content (path, size, mtime), so
// duplicates and moved projects share them. Generation runs one job at a time at the
// lowest priority and is suspended by the work gate while the user plays or scrubs.
package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"framestudio-proto/src/workgate"
)

// FormatVersion is part of the cache key; bump it when the proxy format changes.
const FormatVersion = 1

// Height is the proxy picture height (sources below it keep their size).
const Height = 540

// Mode is the user setting.
const (
	ModeAuto   = "auto"
	ModeOff    = "off"
	ModeAlways = "always"
)

// Info describes a ready proxy.
type Info struct {
	Path   string // the proxy file
	W, H   int
	Frames int
}

// State of one source's proxy.
type State int

const (
	StateNone State = iota
	StateQueued
	StateRunning
	StateReady
	StateFailed
)

// Status is the state plus progress, for the library badge.
type Status struct {
	State   State
	Percent int
}

// Source is what the candidate decision and verification need to know about a video.
type Source struct {
	Codec    string
	PixFmt   string
	W, H     int
	Bitrate  int64   // bits per second, 0 if unknown
	FPS      float64 // 0 if unknown
	Frames   int     // 0 if unknown
	Duration float64 // seconds
}

// ProbeFunc inspects a source.
type ProbeFunc func(ctx context.Context, path string) (Source, error)

// Config creates a Manager.
type Config struct {
	Dir     string // default: user cache dir/framestudio/proxies
	FFmpeg  string // default "ffmpeg"
	FFprobe string // default "ffprobe"
	Gate    workgate.Gate
	Probe   ProbeFunc // default: ffprobe
	Mode    string    // default auto
	CapGB   int       // default 10
	// IdleDelay is how long the gate must stay idle before a job starts (default 1.5 s).
	IdleDelay time.Duration
	Now       func() time.Time
}

// Manager generates and serves proxies. Safe for concurrent use.
type Manager struct {
	cfg Config
	dir string

	mu      sync.Mutex
	mode    string
	capByte int64
	jobs    map[string]*job // by source path
	queue   []*job
	cur     *job
	cache   map[string]*lookup
	closed  bool
	cancel  context.CancelFunc
	ctx     context.Context
	wake    chan struct{}
	done    chan struct{}
	onState func(path string)
	touched map[string]time.Time
	evicted map[string]bool   // abs source path -> proxy trimmed by the size cap: not rebuilt until the cap changes
	skipped map[string]string // path -> source signature+mode already judged not worth a proxy
	fsMu    sync.Mutex        // finishing a proxy and Cleanup must not interleave
}

type job struct {
	path    string
	state   State
	percent int
	force   bool
	cancel  context.CancelFunc
}

type lookup struct {
	sig     string // size|mtime of the source
	key     string
	info    Info
	ok      bool
	checked time.Time
}

type sidecar struct {
	SrcPath  string    `json:"srcPath"`
	W        int       `json:"w"`
	H        int       `json:"h"`
	FPS      float64   `json:"fps"`
	Frames   int       `json:"frames"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed"`
	Version  int       `json:"version"`
	Failed   bool      `json:"failed,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// DefaultDir is the proxy cache directory (honours XDG_CACHE_HOME).
func DefaultDir() string {
	d, err := os.UserCacheDir()
	if err != nil || d == "" {
		d = os.TempDir()
	}
	return filepath.Join(d, "framestudio", "proxies")
}

// New creates a manager and starts its worker goroutine. Close stops it.
func New(cfg Config) *Manager {
	if cfg.Dir == "" {
		cfg.Dir = DefaultDir()
	}
	if cfg.FFmpeg == "" {
		cfg.FFmpeg = "ffmpeg"
	}
	if cfg.FFprobe == "" {
		cfg.FFprobe = "ffprobe"
	}
	if cfg.Gate == nil {
		cfg.Gate = workgate.NopGate{}
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeAuto
	}
	if cfg.CapGB <= 0 {
		cfg.CapGB = 10
	}
	if cfg.IdleDelay == 0 {
		cfg.IdleDelay = 1500 * time.Millisecond
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{cfg: cfg, dir: cfg.Dir, mode: cfg.Mode, capByte: int64(cfg.CapGB) << 30,
		jobs: map[string]*job{}, cache: map[string]*lookup{}, touched: map[string]time.Time{}, skipped: map[string]string{}, evicted: map[string]bool{},
		ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	if cfg.Probe == nil {
		m.cfg.Probe = m.ffprobe
	}
	go m.worker()
	return m
}

// Close cancels the running job and stops the worker.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	m.cancel()
	<-m.done
}

// OnStatus registers a callback for status changes (queued, progress steps, ready, failed).
// It runs on the worker goroutine; hop to the UI thread yourself.
func (m *Manager) OnStatus(fn func(path string)) {
	m.mu.Lock()
	m.onState = fn
	m.mu.Unlock()
}

func (m *Manager) notify(path string) {
	m.mu.Lock()
	fn := m.onState
	m.mu.Unlock()
	if fn != nil {
		fn(path)
	}
}

// SetMode sets auto, off or always. Off hides existing proxies too.
func (m *Manager) SetMode(mode string) {
	switch mode {
	case ModeAuto, ModeOff, ModeAlways:
	default:
		mode = ModeAuto
	}
	m.mu.Lock()
	m.mode = mode
	m.mu.Unlock()
}

// Mode reports the current mode.
func (m *Manager) Mode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

// SetCapGB sets the size limit and trims the cache to it in the background.
func (m *Manager) SetCapGB(gb int) {
	if gb <= 0 {
		gb = 10
	}
	m.setCapBytes(int64(gb) << 30)
}

func (m *Manager) setCapBytes(b int64) {
	m.mu.Lock()
	m.capByte = b
	clear(m.evicted)
	m.mu.Unlock()
	go m.Cleanup()
}

// sourceKey identifies a source by content.
func sourceKey(path string, size int64, mtimeNs int64) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%d", path, size, mtimeNs, FormatVersion)))
	return hex.EncodeToString(h[:])[:20]
}

func (m *Manager) stat(path string) (abs, key, sig string, err error) {
	abs, aerr := filepath.Abs(path)
	if aerr != nil {
		abs = path
	}
	st, err := os.Stat(abs)
	if err != nil {
		return abs, "", "", err
	}
	return abs, sourceKey(abs, st.Size(), st.ModTime().UnixNano()), fmt.Sprintf("%d|%d", st.Size(), st.ModTime().UnixNano()), nil
}

func (m *Manager) files(key string) (mp4, js string) {
	return filepath.Join(m.dir, key+".mp4"), filepath.Join(m.dir, key+".json")
}

func readSidecar(path string) (sidecar, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return sidecar{}, false
	}
	var s sidecar
	if json.Unmarshal(b, &s) != nil {
		return sidecar{}, false
	}
	return s, true
}

func writeSidecar(path string, s sidecar) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Lookup returns the proxy of path when one is ready and valid. It is cheap enough for
// every frame request (a map hit; the source is re-stat'ed every two seconds).
func (m *Manager) Lookup(path string) (Info, bool) {
	m.mu.Lock()
	if m.mode == ModeOff || m.closed {
		m.mu.Unlock()
		return Info{}, false
	}
	now := m.cfg.Now()
	if l := m.cache[path]; l != nil && now.Sub(l.checked) < 2*time.Second {
		info, ok := l.info, l.ok
		m.mu.Unlock()
		return info, ok
	}
	m.mu.Unlock()

	_, key, sig, err := m.stat(path)
	l := &lookup{sig: sig, key: key, checked: now}
	if err == nil {
		mp4, js := m.files(key)
		if sc, ok := readSidecar(js); ok && !sc.Failed && sc.Version == FormatVersion {
			if st, err := os.Stat(mp4); err == nil && st.Size() > 0 {
				l.info = Info{Path: mp4, W: sc.W, H: sc.H, Frames: sc.Frames}
				l.ok = true
				m.touch(key, js, sc, now)
			}
		}
	}
	m.mu.Lock()
	m.cache[path] = l
	m.mu.Unlock()
	return l.info, l.ok
}

// touch refreshes lastUsed at most once per hour per proxy.
func (m *Manager) touch(key, js string, sc sidecar, now time.Time) {
	m.mu.Lock()
	last := m.touched[key]
	if !last.IsZero() && now.Sub(last) < time.Hour {
		m.mu.Unlock()
		return
	}
	m.touched[key] = now
	m.mu.Unlock()
	if now.Sub(sc.LastUsed) < time.Hour {
		return
	}
	sc.LastUsed = now
	go func() { _ = writeSidecar(js, sc) }()
}

func (m *Manager) invalidate(path string) {
	m.mu.Lock()
	delete(m.cache, path)
	m.mu.Unlock()
}

// Status reports the state of path's proxy.
func (m *Manager) Status(path string) Status {
	m.mu.Lock()
	if j := m.jobs[path]; j != nil {
		st := Status{State: j.state, Percent: j.percent}
		m.mu.Unlock()
		return st
	}
	m.mu.Unlock()
	if _, ok := m.Lookup(path); ok {
		return Status{State: StateReady, Percent: 100}
	}
	if _, key, _, err := m.stat(path); err == nil {
		if sc, ok := readSidecar(filepath.Join(m.dir, key+".json")); ok && sc.Failed {
			return Status{State: StateFailed}
		}
	}
	return Status{}
}

// Want queues a proxy for path if it is a candidate and none exists. Idempotent and
// cheap; the candidate decision (an ffprobe run) happens on the worker.
func (m *Manager) Want(path string) { m.want(path, false) }

// Regenerate queues a proxy again, also after a failure or when it exists ("Proxy neu
// erzeugen").
func (m *Manager) Regenerate(path string) {
	m.removeFiles(path)
	m.want(path, true)
}

func (m *Manager) want(path string, force bool) {
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	m.mu.Lock()
	if force {
		delete(m.evicted, abs)
	}
	if m.closed || (m.mode == ModeOff && !force) || m.jobs[path] != nil || m.evicted[abs] {
		m.mu.Unlock()
		return
	}
	j := &job{path: path, state: StateQueued, force: force}
	m.jobs[path] = j
	m.queue = append(m.queue, j)
	m.mu.Unlock()
	m.notify(path)
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Cancel drops the queued or running job of path.
func (m *Manager) Cancel(path string) {
	m.mu.Lock()
	j := m.jobs[path]
	if j == nil {
		m.mu.Unlock()
		return
	}
	delete(m.jobs, path)
	for i, q := range m.queue {
		if q == j {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			break
		}
	}
	cancel := j.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.notify(path)
}

func (m *Manager) removeFiles(path string) {
	m.Cancel(path)
	m.invalidate(path)
	if _, key, _, err := m.stat(path); err == nil {
		mp4, js := m.files(key)
		_ = os.Remove(mp4)
		_ = os.Remove(js)
		_ = os.Remove(mp4 + ".part")
	}
	m.notify(path)
}

// Remove deletes the proxy of path (library context menu "Proxy entfernen").
func (m *Manager) Remove(path string) { m.removeFiles(path) }

// Clear deletes every proxy ("Proxy-Cache leeren") and cancels all jobs.
func (m *Manager) Clear() {
	m.mu.Lock()
	var paths []string
	for p := range m.jobs {
		paths = append(paths, p)
	}
	m.mu.Unlock()
	for _, p := range paths {
		m.Cancel(p)
	}
	ents, _ := os.ReadDir(m.dir)
	for _, e := range ents {
		_ = os.Remove(filepath.Join(m.dir, e.Name()))
	}
	m.mu.Lock()
	m.cache = map[string]*lookup{}
	m.mu.Unlock()
}

// Candidate decides whether a source is worth a proxy in the given mode.
func Candidate(s Source, mode string) bool {
	if mode == ModeOff || s.W <= 0 || s.H <= 0 || HasAlpha(s.PixFmt) {
		return false // the proxy codec has no alpha: overlays would turn opaque
	}
	if mode == ModeAlways {
		return true
	}
	if s.H > 720 {
		return true
	}
	switch s.Codec {
	case "hevc", "av1", "vp9":
		return true
	}
	if strings.Contains(s.PixFmt, "10") || strings.Contains(s.PixFmt, "12") || strings.Contains(s.PixFmt, "422") || strings.Contains(s.PixFmt, "444") {
		return true
	}
	return s.Bitrate > 25_000_000
}

// HasAlpha reports whether an ffmpeg pixel format carries an alpha channel.
func HasAlpha(pixFmt string) bool {
	for _, p := range []string{"yuva", "gbrap", "rgba", "bgra", "argb", "abgr", "ya8", "ya16", "ayuv", "pal8"} {
		if strings.HasPrefix(pixFmt, p) {
			return true
		}
	}
	return false
}

// ffmpegArgs builds the generation command (exported for tests through args()).
func ffmpegArgs(src string, s Source, out string) []string {
	a := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", src,
		"-map", "0:v:0", "-an", "-sn", "-dn"}
	if s.H > Height {
		a = append(a, "-vf", fmt.Sprintf("scale=-2:%d:flags=bicubic", Height))
	} else {
		a = append(a, "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2")
	}
	return append(a, "-fps_mode", "passthrough", "-c:v", "libx264", "-g", "1", "-preset", "veryfast",
		"-crf", "23", "-pix_fmt", "yuv420p", "-threads", "2", "-progress", "pipe:1", "-nostats", "-f", "mp4", out)
}

func (m *Manager) worker() {
	defer close(m.done)
	for {
		m.mu.Lock()
		var j *job
		if len(m.queue) > 0 {
			j = m.queue[0]
			m.queue = m.queue[1:]
		}
		m.mu.Unlock()
		if j == nil {
			select {
			case <-m.ctx.Done():
				return
			case <-m.wake:
			}
			continue
		}
		if m.ctx.Err() != nil {
			return
		}
		m.run(j)
	}
}

// waitIdle blocks until background work is allowed and stays allowed for IdleDelay.
func (m *Manager) waitIdle(ctx context.Context) (release func(), err error) {
	for {
		rel, err := m.cfg.Gate.Acquire(ctx, workgate.PrioBackground)
		if err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			rel()
			return nil, ctx.Err()
		case <-time.After(m.cfg.IdleDelay):
		}
		if !m.cfg.Gate.ShouldYield(workgate.PrioBackground) {
			return rel, nil
		}
		rel()
	}
}

func (m *Manager) setJob(j *job, st State, pct int) {
	m.mu.Lock()
	changed := j.state != st || j.percent != pct
	j.state, j.percent = st, pct
	m.mu.Unlock()
	if changed {
		m.notify(j.path)
	}
}

func (m *Manager) finishJob(j *job) {
	m.mu.Lock()
	if m.jobs[j.path] == j {
		delete(m.jobs, j.path)
	}
	m.cur = nil
	m.mu.Unlock()
}

func (m *Manager) run(j *job) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.mu.Lock()
	if m.jobs[j.path] != j { // cancelled while queued
		m.mu.Unlock()
		cancel()
		return
	}
	j.cancel = cancel
	m.cur = j
	mode := m.mode
	m.mu.Unlock()
	defer cancel()
	defer func() {
		m.finishJob(j)
		m.notify(j.path)
		m.Cleanup()
	}()

	abs, key, sig, err := m.stat(j.path)
	if err != nil {
		return
	}
	mp4, js := m.files(key)
	m.mu.Lock()
	judged := m.skipped[abs] == sig+"|"+mode
	m.mu.Unlock()
	if judged && !j.force {
		return
	}
	if !j.force {
		if sc, ok := readSidecar(js); ok && sc.Version == FormatVersion {
			if sc.Failed {
				return // no retry until the source changes
			}
			if st, err := os.Stat(mp4); err == nil && st.Size() > 0 {
				return // exists
			}
		}
	}
	rel, err := m.waitIdle(ctx)
	if err != nil {
		return
	}
	defer rel()
	src, err := m.cfg.Probe(ctx, abs)
	if err != nil {
		if ctx.Err() == nil {
			m.fail(js, abs, err)
		}
		return
	}
	if HasAlpha(src.PixFmt) || (!j.force && !Candidate(src, mode)) {
		m.mu.Lock()
		m.skipped[abs] = sig + "|" + mode
		m.mu.Unlock()
		return
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return
	}
	m.setJob(j, StateRunning, 0)
	part := mp4 + ".part"
	_ = os.Remove(part)
	frames, w, h, err := m.generate(ctx, j, abs, src, part)
	if err != nil {
		_ = os.Remove(part)
		if ctx.Err() == nil {
			m.fail(js, abs, err)
		}
		return
	}
	m.fsMu.Lock()
	if err := os.Rename(part, mp4); err != nil {
		m.fsMu.Unlock()
		_ = os.Remove(part)
		return
	}
	now := m.cfg.Now()
	_ = writeSidecar(js, sidecar{SrcPath: abs, W: w, H: h, FPS: src.FPS, Frames: frames,
		Created: now, LastUsed: now, Version: FormatVersion})
	m.fsMu.Unlock()
	m.invalidate(j.path)
}

func (m *Manager) fail(js, abs string, err error) {
	_ = os.MkdirAll(m.dir, 0o755)
	now := m.cfg.Now()
	_ = writeSidecar(js, sidecar{SrcPath: abs, Created: now, LastUsed: now, Version: FormatVersion,
		Failed: true, Error: err.Error()})
	m.invalidate(abs)
}

// generate runs ffmpeg and verifies the result; it returns the proxy's frame count and size.
func (m *Manager) generate(ctx context.Context, j *job, src string, s Source, out string) (frames, w, h int, err error) {
	cmd := m.cfg.Gate.Command(ctx, workgate.PrioBackground, m.cfg.FFmpeg, ffmpegArgs(src, s, out)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, 0, 0, err
	}
	done, err := workgate.StartCmd(m.cfg.Gate, cmd, workgate.PrioBackground)
	if err != nil {
		return 0, 0, 0, err
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok || (k != "out_time_us" && k != "out_time_ms") || s.Duration <= 0 {
			continue
		}
		us, _ := strconv.ParseFloat(v, 64)
		pct := int(math.Min(99, us/1e6/s.Duration*100))
		if pct >= j.percent+2 {
			m.setJob(j, StateRunning, pct)
		}
	}
	werr := cmd.Wait()
	done()
	if ctx.Err() != nil {
		return 0, 0, 0, ctx.Err()
	}
	if werr != nil {
		return 0, 0, 0, fmt.Errorf("ffmpeg: %s", firstLine(stderr.String(), werr))
	}
	pf, pw, ph, err := m.countFrames(ctx, out)
	if err != nil {
		return 0, 0, 0, err
	}
	want := s.Frames
	if want <= 0 && s.FPS > 0 && s.Duration > 0 {
		want = int(math.Round(s.FPS * s.Duration))
		if abs(pf-want) <= 1 {
			want = pf
		}
	}
	if want > 0 && pf != want {
		return 0, 0, 0, fmt.Errorf("Proxy hat %d statt %d Bilder", pf, want)
	}
	return pf, pw, ph, nil
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func firstLine(s string, err error) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" && err != nil {
		s = err.Error()
	}
	return s
}

type probeJSON struct {
	Streams []struct {
		CodecName    string `json:"codec_name"`
		PixFmt       string `json:"pix_fmt"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		BitRate      string `json:"bit_rate"`
		RFrameRate   string `json:"r_frame_rate"`
		AvgFrameRate string `json:"avg_frame_rate"`
		NbFrames     string `json:"nb_frames"`
		NbRead       string `json:"nb_read_frames"`
		Duration     string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

func parseRatio(s string) float64 {
	n, d, ok := strings.Cut(s, "/")
	if !ok {
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	a, _ := strconv.ParseFloat(n, 64)
	b, _ := strconv.ParseFloat(d, 64)
	if b == 0 {
		return 0
	}
	return a / b
}

func (m *Manager) runProbe(ctx context.Context, args ...string) (probeJSON, error) {
	var out probeJSON
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := m.cfg.Gate.Command(ctx, workgate.PrioBackground, m.cfg.FFprobe, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	done, err := workgate.StartCmd(m.cfg.Gate, cmd, workgate.PrioBackground)
	if err != nil {
		return out, err
	}
	err = cmd.Wait()
	done()
	if err != nil {
		return out, fmt.Errorf("ffprobe: %s", firstLine(stderr.String(), err))
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return out, err
	}
	if len(out.Streams) == 0 {
		return out, errors.New("kein Videostrom")
	}
	return out, nil
}

func (m *Manager) ffprobe(ctx context.Context, path string) (Source, error) {
	p, err := m.runProbe(ctx, "-v", "error", "-select_streams", "v:0", "-show_entries",
		"stream=codec_name,pix_fmt,width,height,bit_rate,r_frame_rate,avg_frame_rate,nb_frames,duration:format=duration,bit_rate",
		"-of", "json", path)
	if err != nil {
		return Source{}, err
	}
	st := p.Streams[0]
	s := Source{Codec: st.CodecName, PixFmt: st.PixFmt, W: st.Width, H: st.Height}
	s.Bitrate, _ = strconv.ParseInt(st.BitRate, 10, 64)
	if s.Bitrate == 0 {
		s.Bitrate, _ = strconv.ParseInt(p.Format.BitRate, 10, 64)
	}
	s.FPS = parseRatio(st.AvgFrameRate)
	if s.FPS == 0 {
		s.FPS = parseRatio(st.RFrameRate)
	}
	s.Frames, _ = strconv.Atoi(st.NbFrames)
	s.Duration, _ = strconv.ParseFloat(st.Duration, 64)
	if s.Duration == 0 {
		s.Duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	}
	return s, nil
}

func (m *Manager) countFrames(ctx context.Context, path string) (frames, w, h int, err error) {
	p, err := m.runProbe(ctx, "-v", "error", "-select_streams", "v:0", "-count_frames", "-show_entries",
		"stream=width,height,nb_read_frames", "-of", "json", path)
	if err != nil {
		return 0, 0, 0, err
	}
	st := p.Streams[0]
	frames, _ = strconv.Atoi(st.NbRead)
	if frames <= 0 {
		return 0, 0, 0, errors.New("Proxy enthält keine Bilder")
	}
	return frames, st.Width, st.Height, nil
}

// Cleanup removes leftovers and trims the cache to its size cap: .part files of no
// running job, orphans without sidecar or media file, proxies of vanished sources unused
// for 30 days, then least recently used proxies until the cap holds.
func (m *Manager) Cleanup() {
	m.fsMu.Lock()
	defer m.fsMu.Unlock()
	ents, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	m.mu.Lock()
	capB := m.capByte
	now := m.cfg.Now()
	running := m.cur != nil
	inUse := map[string]bool{} // proxies looked up in this session: playback reads them
	for _, l := range m.cache {
		if l.ok {
			inUse[l.key] = true
		}
	}
	m.mu.Unlock()

	type entry struct {
		key, src string
		size     int64
		last     time.Time
		fresh    bool
	}
	var keep []entry
	var total int64
	known := map[string]bool{}
	for _, e := range ents {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".part"):
			if !running {
				_ = os.Remove(filepath.Join(m.dir, name))
			}
		case strings.HasSuffix(name, ".mp4"):
			known[strings.TrimSuffix(name, ".mp4")] = true
		case strings.HasSuffix(name, ".json.tmp") || strings.HasSuffix(name, ".mp4.tmp"):
			_ = os.Remove(filepath.Join(m.dir, name))
		}
	}
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		key := strings.TrimSuffix(name, ".json")
		sc, ok := readSidecar(filepath.Join(m.dir, name))
		if !ok || (!sc.Failed && !known[key]) {
			_ = os.Remove(filepath.Join(m.dir, name))
			continue
		}
		if sc.Failed {
			continue
		}
		known[key] = false // paired
		mp4 := filepath.Join(m.dir, key+".mp4")
		st, err := os.Stat(mp4)
		if err != nil {
			continue
		}
		if _, err := os.Stat(sc.SrcPath); err != nil && now.Sub(sc.LastUsed) > 30*24*time.Hour {
			_ = os.Remove(mp4)
			_ = os.Remove(filepath.Join(m.dir, name))
			continue
		}
		keep = append(keep, entry{key, sc.SrcPath, st.Size(), sc.LastUsed, now.Sub(sc.Created) < 10*time.Minute})
		total += st.Size()
	}
	for key, orphan := range known { // media file without sidecar
		if orphan {
			_ = os.Remove(filepath.Join(m.dir, key+".mp4"))
		}
	}
	if total <= capB {
		return
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].last.Before(keep[j].last) })
	for _, e := range keep {
		if total <= capB {
			break
		}
		if e.fresh || inUse[e.key] {
			continue // just made or being read: removing it would only make it rebuild at once
		}
		_ = os.Remove(filepath.Join(m.dir, e.key+".mp4"))
		_ = os.Remove(filepath.Join(m.dir, e.key+".json"))
		total -= e.size
		m.mu.Lock()
		m.evicted[e.src] = true
		m.cache = map[string]*lookup{} // lookups of the removed proxy are stale
		m.mu.Unlock()
	}
}

// StartCleanup runs Cleanup once after delay in the background (startup: 30 s).
func (m *Manager) StartCleanup(delay time.Duration) {
	go func() {
		select {
		case <-time.After(delay):
			m.Cleanup()
		case <-m.ctx.Done():
		}
	}()
}

// FFmpegMissing reports whether the configured ffmpeg cannot be found.
func (m *Manager) FFmpegMissing() bool {
	_, err := exec.LookPath(m.cfg.FFmpeg)
	return err != nil
}
