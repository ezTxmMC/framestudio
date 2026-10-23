package media

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"framestudio-proto/src/media/hwaccel"
	"framestudio-proto/src/media/proxy"
	"framestudio-proto/src/model"
	"framestudio-proto/src/workgate"
)

// SourceKind says how a frame was produced; the viewer's statistics show the dominant one.
type SourceKind int

const (
	SrcSW    SourceKind = iota // software decode of the original
	SrcHW                      // hardware decode of the original
	SrcProxy                   // decoded from a proxy
	SrcCache                   // served from a cache above the decoders
)

func (k SourceKind) String() string {
	switch k {
	case SrcHW:
		return "hw"
	case SrcProxy:
		return "proxy"
	case SrcCache:
		return "cache"
	}
	return "sw"
}

// bgThreads is the decoder thread count of background ffmpeg runs.
const bgThreads = 2

type gateBox struct{ g workgate.Gate }
type proxyBox struct{ p ProxyLookup }
type hwBox struct{ h HWPlanner }

// ProxyLookup is the part of *proxy.Manager the cache needs.
type ProxyLookup interface {
	Lookup(path string) (proxy.Info, bool)
	Want(path string)
	Mode() string
}

// HWPlanner is the part of *hwaccel.Manager the cache needs.
type HWPlanner interface {
	Plan(path string, src hwaccel.Info, w, h int) (hwaccel.Args, bool)
	Fail(path string, src hwaccel.Info)
	Info() hwaccel.Result
}

// SetGate sets the work gate for background ffmpeg jobs (thumbnails, posters, waveforms,
// keyframe indexes). The default never blocks.
func (c *Cache) SetGate(g workgate.Gate) {
	if g == nil {
		g = workgate.NopGate{}
	}
	c.gate.Store(&gateBox{g})
}

func (c *Cache) gateOf() workgate.Gate {
	if b := c.gate.Load(); b != nil {
		return b.g
	}
	return workgate.NopGate{}
}

// SetProxies enables transparent use of proxies for preview decoding (nil = never).
func (c *Cache) SetProxies(p ProxyLookup) {
	if p == nil {
		c.proxies.Store(nil)
		return
	}
	c.proxies.Store(&proxyBox{p})
}

// SetHW enables hardware decoding where the planner approves (nil = never).
func (c *Cache) SetHW(h HWPlanner) {
	if h == nil {
		c.hwp.Store(nil)
		return
	}
	c.hwp.Store(&hwBox{h})
}

func (c *Cache) hwPlanner() HWPlanner {
	if b := c.hwp.Load(); b != nil {
		return b.h
	}
	return noHW{}
}

type noHW struct{}

func (noHW) Plan(string, hwaccel.Info, int, int) (hwaccel.Args, bool) { return hwaccel.Args{}, false }
func (noHW) Fail(string, hwaccel.Info)                                {}
func (noHW) Info() hwaccel.Result                                     { return hwaccel.Result{Kind: hwaccel.None} }

// SetShuttleMode forces "keyframes" (keyframe-only shuttle at 4x and above) or "auto"
// (exact when decoding keeps up).
func (c *Cache) SetShuttleMode(mode string) { c.shuttleKF.Store(mode == "keyframes") }

// proxyFor chooses the proxy file for a preview request, or "" for the original.
// Auto mode uses a proxy that exists when the picture is small (render height up to
// 600 px, i.e. any half/quarter quality or a 4K source in a small viewer), when scrubbing,
// or when not playing at normal speed; always mode whenever one exists. Accurate requests
// (export, stills refine) never use proxies.
func (c *Cache) proxyFor(m model.MediaItem, hint MediaHint, h int) string {
	b := c.proxies.Load()
	if b == nil || hint.Accurate || m.Kind != model.KindVideo {
		return ""
	}
	info, ok := b.p.Lookup(m.Path)
	if !ok {
		b.p.Want(m.Path)
		return ""
	}
	if t, bad := c.badProxy.Load(info.Path); bad {
		if time.Since(t.(time.Time)) < time.Minute {
			return ""
		}
		c.badProxy.Delete(info.Path)
	}
	renderH := hint.RenderH
	if renderH <= 0 {
		renderH = h
	}
	speed := hint.Speed
	use := b.p.Mode() == proxy.ModeAlways || renderH <= 600 || hint.Scrub ||
		(speed != 0 && (speed < 0.9 || speed > 1.1))
	if !use {
		return ""
	}
	return info.Path
}

// srcInfoEntry is the codec/pixel format of a source, probed once.
type srcInfoEntry struct {
	once sync.Once
	info hwaccel.Info
	ok   bool
}

type ffprobeStream struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
		PixFmt    string `json:"pix_fmt"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
}

func (c *Cache) forgetSrcInfo(id string) {
	c.srcInfos.Range(func(k, _ any) bool {
		if strings.HasPrefix(k.(string), id+"|") {
			c.srcInfos.Delete(k)
		}
		return true
	})
}

// sourceInfo returns codec and pixel format of m, asking ffprobe once.
func (c *Cache) sourceInfo(m model.MediaItem) (hwaccel.Info, bool) {
	v, _ := c.srcInfos.LoadOrStore(m.ID+"|"+m.Path, &srcInfoEntry{})
	e := v.(*srcInfoEntry)
	e.once.Do(func() {
		lookTools()
		if tools.ffprobe == "" {
			return
		}
		ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
		defer cancel()
		var out bytes.Buffer
		cmd := exec.CommandContext(ctx, tools.ffprobe, "-v", "error", "-select_streams", "v:0",
			"-show_entries", "stream=codec_name,pix_fmt,width,height", "-of", "json", m.Path)
		cmd.Stdout = &out
		if cmd.Run() != nil {
			return
		}
		var p ffprobeStream
		if json.Unmarshal(out.Bytes(), &p) != nil || len(p.Streams) == 0 {
			return
		}
		st := p.Streams[0]
		e.info = hwaccel.Info{Codec: st.CodecName, PixFmt: st.PixFmt, W: st.Width, H: st.Height}
		e.ok = true
	})
	return e.info, e.ok
}

// hwPlan decides whether stream s of m is decoded in hardware. Proxies, small pictures,
// rotated files and anything the planner refuses are software.
func (c *Cache) hwPlan(m model.MediaItem, s stream) (hwaccel.Args, hwaccel.Info, bool) {
	b := c.hwp.Load()
	if b == nil || s.proxy != "" || b.h.Info().Kind == hwaccel.None {
		return hwaccel.Args{}, hwaccel.Info{}, false
	}
	info, ok := c.sourceInfo(m)
	if !ok {
		return hwaccel.Args{}, hwaccel.Info{}, false
	}
	if info.W != m.Width || info.H != m.Height { // rotated: the filter graph turns the picture
		return hwaccel.Args{}, hwaccel.Info{}, false
	}
	args, ok := b.h.Plan(m.Path, info, s.w, s.h)
	return args, info, ok
}

// maxDecoderThreads caps the threads of one playback decoder. Measured on a 4K 80 Mbit
// H.264 file: 4 threads are 1.7x slower than ffmpeg's default (one per CPU), 8 threads
// 1.2x, so the cap is generous; the share per decoder is what protects the compositor.
const maxDecoderThreads = 12

// decoderThreads is the ffmpeg thread count of a playback decoder: the CPUs shared by
// the decoders that run now, between two and maxDecoderThreads.
func (c *Cache) decoderThreads() int {
	c.pmu.Lock()
	n := len(c.prefetchers)
	c.pmu.Unlock()
	return min(max(runtime.NumCPU()/max(n, 1), 2), maxDecoderThreads)
}

// DecoderLimit bounds live decoder processes: playback decoders plus the scrub decoder.
func (c *Cache) DecoderLimit() int { return c.maxDecoders() + 1 }

// LiveDecoders counts running prefetching decoders (tests and statistics).
func (c *Cache) LiveDecoders() int {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	n := 0
	for _, p := range c.prefetchers {
		if !p.isDone() {
			n++
		}
	}
	return n
}
