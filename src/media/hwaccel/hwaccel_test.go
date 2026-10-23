package hwaccel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRun answers like an ffmpeg with VAAPI and libx264.
func fakeRun(failDecode bool, calls *[]string) RunFunc {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		j := strings.Join(args, " ")
		*calls = append(*calls, j)
		switch {
		case strings.Contains(j, "-version"):
			return []byte("ffmpeg version fake-1\nbuilt with x\n"), nil
		case strings.Contains(j, "-hwaccels"):
			return []byte("Hardware acceleration methods:\nvdpau\nvaapi\ncuda\n"), nil
		case strings.Contains(j, "-encoders"):
			return []byte("Encoders:\n V....D libx264              H.264\n V....D libx265  HEVC\n A....D aac aac\n"), nil
		case strings.Contains(j, "lavfi"):
			// the output file is the last argument
			return nil, os.WriteFile(args[len(args)-1], []byte("x"), 0o644)
		case strings.Contains(j, "-hwaccel vaapi"):
			if failDecode || strings.Contains(j, "probe_hevc") {
				return []byte("Failed to initialise VAAPI"), os.ErrInvalid
			}
			return nil, nil
		}
		return nil, os.ErrInvalid
	}
}

func fakeProber(t *testing.T, failDecode bool, calls *[]string) *Prober {
	dev := filepath.Join(t.TempDir(), "renderD128")
	if err := os.WriteFile(dev, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return &Prober{CacheDir: t.TempDir(), Run: fakeRun(failDecode, calls),
		Devices: func() []string { return []string{dev} }, Nvidia: func() bool { return false }}
}

func TestProbeParsesAndCaches(t *testing.T) {
	var calls []string
	p := fakeProber(t, false, &calls)
	res := p.Probe(context.Background())
	if res.Kind != VAAPI || !res.Codecs["h264"] || res.Codecs["hevc"] {
		t.Fatalf("probe: %+v", res)
	}
	if res.Version != "ffmpeg version fake-1" {
		t.Fatalf("version %q", res.Version)
	}
	n := len(calls)
	res2 := p.Probe(context.Background())
	if res2.Kind != VAAPI || len(calls) != n+1 { // only -version is asked again
		t.Fatalf("cached probe re-ran: %d calls (+%d), %+v", len(calls), len(calls)-n, res2)
	}
}

func TestProbeNoneWhenDecodeFails(t *testing.T) {
	var calls []string
	p := fakeProber(t, true, &calls)
	if res := p.Probe(context.Background()); res.Kind != None {
		t.Fatalf("want none, got %+v", res)
	}
}

func TestProbeNoFFmpeg(t *testing.T) {
	p := &Prober{Run: func(context.Context, string, ...string) ([]byte, error) { return nil, os.ErrNotExist }}
	if res := p.Probe(context.Background()); res.Kind != None {
		t.Fatalf("%+v", res)
	}
}

func TestPlanRules(t *testing.T) {
	res := Result{Kind: VAAPI, Device: "/dev/dri/renderD128", Codecs: map[string]bool{"h264": true}}
	a, ok := Plan(res, Info{"h264", "yuv420p", 3840, 2160}, 1280, 720)
	if !ok || !strings.Contains(a.Filter, "scale_vaapi=w=1280:h=720") || a.Input[1] != "vaapi" {
		t.Fatalf("plan %+v %v", a, ok)
	}
	if a, ok = Plan(res, Info{"h264", "yuv420p", 3840, 2160}, 1281, 721); !ok || strings.Contains(a.Filter, "scale_vaapi") {
		t.Fatalf("odd size must not use scale_vaapi: %+v", a)
	}
	for _, in := range []Info{
		{"h264", "yuv422p10le", 3840, 2160}, // 4:2:2 10 bit
		{"hevc", "yuv420p", 3840, 2160},     // codec not verified
		{"h264", "yuv420p", 640, 360},       // too small
	} {
		if _, ok := Plan(res, in, 640, 360); ok {
			t.Fatalf("plan for %+v", in)
		}
	}
	if _, ok := Plan(Result{Kind: None}, Info{"h264", "yuv420p", 3840, 2160}, 640, 360); ok {
		t.Fatal("plan without hardware")
	}
}

func TestManagerBlacklistAndMode(t *testing.T) {
	var calls []string
	p := fakeProber(t, false, &calls)
	m := NewManager(p)
	m.ProbeNow(context.Background())
	src := Info{"h264", "yuv420p", 3840, 2160}
	if _, ok := m.Plan("/a.mp4", src, 640, 360); !ok {
		t.Fatal("no plan")
	}
	m.SetMode("off")
	if _, ok := m.Plan("/a.mp4", src, 640, 360); ok {
		t.Fatal("plan in mode off")
	}
	m.SetMode("auto")
	m.Fail("/a.mp4", src)
	if _, ok := m.Plan("/a.mp4", src, 640, 360); ok || !m.Banned("/a.mp4") {
		t.Fatal("failed file not blacklisted")
	}
	if _, ok := m.Plan("/b.mp4", src, 640, 360); !ok {
		t.Fatal("other file must stay hardware")
	}
	m.Fail("/b.mp4", src)
	m.Fail("/c.mp4", src)
	if _, ok := m.Plan("/d.mp4", src, 640, 360); ok {
		t.Fatal("combo not banned after repeated failures")
	}
	// the negative entry survives a restart
	m2 := NewManager(p)
	m2.ProbeNow(context.Background())
	if _, ok := m2.Plan("/d.mp4", src, 640, 360); ok {
		t.Fatal("negative entry not persisted")
	}
}

func TestManagerStartIsAsync(t *testing.T) {
	var calls []string
	m := NewManager(fakeProber(t, false, &calls))
	m.Start(0)
	m.Start(0)
	end := time.Now().Add(2 * time.Second)
	for m.Info().Kind == None && time.Now().Before(end) {
		time.Sleep(5 * time.Millisecond)
	}
	if m.Info().Kind != VAAPI {
		t.Fatal("background probe did not finish")
	}
}

// With a real ffmpeg and render node the probe must finish and give a consistent answer.
func TestRealProbe(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	p := &Prober{CacheDir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	res := p.Probe(ctx)
	t.Logf("real probe: kind=%s device=%s codecs=%v", res.Kind, res.Device, res.Codecs)
	if res.Kind != None && len(res.Codecs) == 0 {
		t.Fatalf("hardware without codecs: %+v", res)
	}
}
