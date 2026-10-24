// Package hwaccel detects hardware video decoding (VAAPI over a render node, NVDEC) and
// builds ffmpeg command lines for it. Nothing here opens a display: only render nodes
// and ffmpeg are used. Every failure degrades to software decoding.
package hwaccel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind is the hardware decoder family.
type Kind string

const (
	None         Kind = "none"
	VAAPI        Kind = "vaapi"
	NVDEC        Kind = "nvdec"
	VideoToolbox Kind = "videotoolbox"
)

// Result is what the probe found.
type Result struct {
	Kind    Kind            `json:"kind"`
	Device  string          `json:"device,omitempty"`
	Codecs  map[string]bool `json:"codecs,omitempty"` // codec name as ffprobe reports it (h264, hevc)
	Version string          `json:"version,omitempty"`
}

// Info describes a source for the decision whether and how to decode it in hardware.
type Info struct {
	Codec  string // h264, hevc, ...
	PixFmt string // yuv420p, yuv422p10le, ...
	W, H   int
}

// Args is a hardware decode plan: Input goes before -i, Filter in front of the video
// filter chain (ends with a comma-free chain; the caller adds a comma).
type Args struct {
	Input  []string
	Filter string
}

// RunFunc runs a command and returns its combined output.
type RunFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.Bytes(), err
}

// Prober runs the hardware probe.
type Prober struct {
	FFmpeg   string  // binary, default "ffmpeg"
	CacheDir string  // where probe samples and hwprobe.json live; "" disables persistence
	Run      RunFunc // default: os/exec

	Devices func() []string // render nodes; default /dev/dri/renderD*
	Nvidia  func() bool     // NVIDIA device present; default /dev/nvidia0 exists
}

func (p *Prober) bin() string {
	if p.FFmpeg != "" {
		return p.FFmpeg
	}
	return "ffmpeg"
}

func (p *Prober) run(ctx context.Context, args ...string) ([]byte, error) {
	r := p.Run
	if r == nil {
		r = execRun
	}
	return r(ctx, p.bin(), args...)
}

func (p *Prober) devices() []string {
	if p.Devices != nil {
		return p.Devices()
	}
	m, _ := filepath.Glob("/dev/dri/renderD*")
	sort.Strings(m)
	return m
}

func (p *Prober) nvidia() bool {
	if p.Nvidia != nil {
		return p.Nvidia()
	}
	_, err := os.Stat("/dev/nvidia0")
	return err == nil
}

// listHwaccels parses the output of "ffmpeg -hwaccels".
func listHwaccels(out []byte) map[string]bool {
	m := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasSuffix(l, ":") {
			continue
		}
		m[l] = true
	}
	return m
}

// versionLine returns the first line of "ffmpeg -version".
func versionLine(out []byte) string {
	l, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(l)
}

type cacheFile struct {
	Version   string         `json:"version"`
	DeviceKey string         `json:"deviceKey"`
	Result    Result         `json:"result"`
	Negative  map[string]int `json:"negative,omitempty"` // "codec|pixfmt" -> failures
}

func deviceKey(dev string) string {
	if dev == "" {
		return ""
	}
	st, err := os.Stat(dev)
	if err != nil {
		return dev + "|missing"
	}
	return fmt.Sprintf("%s|%d", dev, st.ModTime().UnixNano())
}

func (p *Prober) cachePath() string {
	if p.CacheDir == "" {
		return ""
	}
	return filepath.Join(p.CacheDir, "hwprobe.json")
}

func (p *Prober) loadCache() (cacheFile, bool) {
	path := p.cachePath()
	if path == "" {
		return cacheFile{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cacheFile{}, false
	}
	var c cacheFile
	if json.Unmarshal(b, &c) != nil {
		return cacheFile{}, false
	}
	return c, true
}

func (p *Prober) saveCache(c cacheFile) {
	path := p.cachePath()
	if path == "" {
		return
	}
	if os.MkdirAll(p.CacheDir, 0o755) != nil {
		return
	}
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

// Probe finds the hardware decoder. It is slow (several ffmpeg runs) and must never run
// on the UI thread. A previous result is reused while ffmpeg and the device are the same.
func (p *Prober) Probe(ctx context.Context) Result {
	res, _ := p.probe(ctx)
	return res
}

func (p *Prober) probe(ctx context.Context) (Result, cacheFile) {
	none := Result{Kind: None}
	vout, err := p.run(ctx, "-hide_banner", "-version")
	if err != nil {
		return none, cacheFile{}
	}
	ver := versionLine(vout)
	devs := p.devices()
	dev := ""
	if len(devs) > 0 {
		dev = devs[0]
	}
	key := deviceKey(dev)
	if p.nvidia() {
		key += "|nvidia"
	}
	if c, ok := p.loadCache(); ok && c.Version == ver && c.DeviceKey == key {
		c.Result.Version = ver
		return c.Result, c
	}
	hw := listHwaccels(mustOut(p.run(ctx, "-hide_banner", "-hwaccels")))
	res := none
	res.Version = ver
	switch {
	case hw["vaapi"] && dev != "":
		if codecs := p.probeCodecs(ctx, vaapiProbeArgs(dev)); len(codecs) > 0 {
			res = Result{Kind: VAAPI, Device: dev, Codecs: codecs, Version: ver}
		}
	}
	if res.Kind == None && hw["cuda"] && p.nvidia() {
		if codecs := p.probeCodecs(ctx, nvdecProbeArgs); len(codecs) > 0 {
			res = Result{Kind: NVDEC, Codecs: codecs, Version: ver}
		}
	}
	c := cacheFile{Version: ver, DeviceKey: key, Result: res}
	if old, ok := p.loadCache(); ok && old.Version == ver {
		c.Negative = old.Negative
	}
	p.saveCache(c)
	return res, c
}

func mustOut(b []byte, _ error) []byte { return b }

func vaapiProbeArgs(dev string) func(sample string) []string {
	return func(sample string) []string {
		return []string{"-nostdin", "-hide_banner", "-v", "error", "-hwaccel", "vaapi", "-hwaccel_device", dev,
			"-hwaccel_output_format", "vaapi", "-i", sample, "-vf", "hwdownload,format=nv12", "-frames:v", "3", "-f", "null", "-"}
	}
}

func nvdecProbeArgs(sample string) []string {
	return []string{"-nostdin", "-hide_banner", "-v", "error", "-hwaccel", "cuda", "-hwaccel_output_format", "cuda",
		"-i", sample, "-vf", "hwdownload,format=nv12", "-frames:v", "3", "-f", "null", "-"}
}

// sample encoders: ffprobe codec name -> ffmpeg encoder
var sampleEncoders = []struct{ codec, encoder string }{
	{"h264", "libx264"},
	{"hevc", "libx265"},
	{"av1", "libaom-av1"},
}

func (p *Prober) probeCodecs(ctx context.Context, decode func(sample string) []string) map[string]bool {
	dir := p.CacheDir
	if dir == "" {
		d, err := os.MkdirTemp("", "fs-hwprobe-")
		if err != nil {
			return nil
		}
		defer os.RemoveAll(d)
		dir = d
	} else if os.MkdirAll(dir, 0o755) != nil {
		return nil
	}
	enc := listEncoders(mustOut(p.run(ctx, "-hide_banner", "-encoders")))
	ok := map[string]bool{}
	for _, se := range sampleEncoders {
		if !enc[se.encoder] || ctx.Err() != nil {
			continue
		}
		sample := filepath.Join(dir, "probe_"+se.codec+".mp4")
		if _, err := os.Stat(sample); err != nil {
			args := []string{"-nostdin", "-hide_banner", "-v", "error", "-y", "-f", "lavfi", "-i",
				"testsrc2=s=256x144:d=1:r=25", "-c:v", se.encoder, "-pix_fmt", "yuv420p"}
			if se.encoder == "libx265" {
				args = append(args, "-x265-params", "log-level=error")
			}
			if se.encoder == "libaom-av1" {
				args = append(args, "-cpu-used", "8", "-threads", "2")
			}
			args = append(args, sample+".tmp.mp4")
			if _, err := p.run(ctx, args...); err != nil {
				_ = os.Remove(sample + ".tmp.mp4")
				continue
			}
			if os.Rename(sample+".tmp.mp4", sample) != nil {
				continue
			}
		}
		out, err := p.run(ctx, decode(sample)...)
		if err == nil && !bytes.Contains(bytes.ToLower(out), []byte("failed")) {
			ok[se.codec] = true
		}
	}
	return ok
}

func listEncoders(out []byte) map[string]bool {
	m := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && len(f[0]) == 6 && strings.ContainsAny(f[0][:1], "VAS") {
			m[f[1]] = true
		}
	}
	return m
}

// supportedPixFmt reports 8-bit 4:2:0 formats, which every hardware decoder takes.
func supportedPixFmt(pf string) bool {
	switch pf {
	case "yuv420p", "yuvj420p", "nv12":
		return true
	}
	return false
}

// Plan builds the decode plan for a source scaled to w x h, if hardware decoding applies.
func Plan(res Result, src Info, w, h int) (Args, bool) {
	if res.Kind == None || res.Kind == "" || !res.Codecs[src.Codec] || !supportedPixFmt(src.PixFmt) {
		return Args{}, false
	}
	if src.W < 1920 && src.H < 1080 { // not worth the overhead for small pictures
		return Args{}, false
	}
	switch res.Kind {
	case VAAPI:
		in := []string{"-hwaccel", "vaapi", "-hwaccel_device", res.Device, "-hwaccel_output_format", "vaapi"}
		if w > 0 && h > 0 && w%2 == 0 && h%2 == 0 {
			return Args{Input: in, Filter: fmt.Sprintf("scale_vaapi=w=%d:h=%d:format=nv12,hwdownload,format=nv12", w, h)}, true
		}
		return Args{Input: in, Filter: "hwdownload,format=nv12"}, true
	case NVDEC:
		return Args{Input: []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"},
			Filter: "hwdownload,format=nv12"}, true
	}
	return Args{}, false
}

// FailureLimit is how many failures of one codec/pixel format combination turn hardware
// decoding off for it, for good.
const FailureLimit = 3

// Manager owns the probe result, the mode and the failure blacklist.
type Manager struct {
	prober *Prober

	mu      sync.RWMutex
	res     Result
	probed  bool
	off     bool
	paths   map[string]bool
	combos  map[string]int
	started bool
}

// NewManager creates a manager. Call Start to probe in the background.
func NewManager(p *Prober) *Manager {
	m := &Manager{prober: p, paths: map[string]bool{}, combos: map[string]int{}, res: Result{Kind: None}}
	return m
}

// Start probes after delay in a goroutine; safe to call more than once.
func (m *Manager) Start(delay time.Duration) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()
	go func() {
		time.Sleep(delay)
		m.ProbeNow(context.Background())
	}()
}

// ProbeNow runs the probe in the calling goroutine (tests, tools).
func (m *Manager) ProbeNow(ctx context.Context) Result {
	res, c := m.prober.probe(ctx)
	m.mu.Lock()
	m.res, m.probed = res, true
	for k, v := range c.Negative {
		m.combos[k] = max(m.combos[k], v)
	}
	m.mu.Unlock()
	return res
}

// Info returns the probe result (None before the probe finished).
func (m *Manager) Info() Result {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.res
}

// SetMode switches hardware decoding: "auto" or "off".
func (m *Manager) SetMode(mode string) {
	m.mu.Lock()
	m.off = mode == "off"
	m.mu.Unlock()
}

func comboKey(i Info) string { return i.Codec + "|" + i.PixFmt }

// Plan returns the hardware plan for path, unless disabled, unsupported or blacklisted.
func (m *Manager) Plan(path string, src Info, w, h int) (Args, bool) {
	m.mu.RLock()
	res, off := m.res, m.off
	banned := m.paths[path] || m.combos[comboKey(src)] >= FailureLimit
	m.mu.RUnlock()
	if off || banned {
		return Args{}, false
	}
	return Plan(res, src, w, h)
}

// Fail records that hardware decoding of path failed; the file is software-only for the
// rest of the session and after FailureLimit failures the codec/pixel format combination
// is stored as unusable.
func (m *Manager) Fail(path string, src Info) {
	m.mu.Lock()
	m.paths[path] = true
	k := comboKey(src)
	m.combos[k]++
	n := m.combos[k]
	m.mu.Unlock()
	if n == FailureLimit {
		c, _ := m.prober.loadCache()
		if c.Negative == nil {
			c.Negative = map[string]int{}
		}
		c.Negative[k] = n
		m.mu.RLock()
		if c.Version == "" {
			c.Version = m.res.Version
			c.Result = m.res
		}
		m.mu.RUnlock()
		m.prober.saveCache(c)
	}
}

// Banned reports whether path is software-only.
func (m *Manager) Banned(path string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.paths[path]
}
