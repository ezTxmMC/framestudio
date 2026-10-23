//go:build !windows

package proxy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"framestudio-proto/src/workgate"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	for _, b := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skip("no " + b)
		}
	}
}

func genSource(t *testing.T, dir string, w, h int, secs string) string {
	t.Helper()
	p := filepath.Join(dir, "src.mp4")
	out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i",
		"testsrc2=s="+itoa(w)+"x"+itoa(h)+":r=30:d="+secs, "-c:v", "libx264", "-preset", "ultrafast",
		"-g", "100", "-pix_fmt", "yuv420p", p).CombinedOutput()
	if err != nil {
		t.Fatalf("gen: %v %s", err, out)
	}
	return p
}

func itoa(i int) string { return strconv.Itoa(i) }

func waitState(t *testing.T, m *Manager, path string, want State, d time.Duration) Status {
	t.Helper()
	end := time.Now().Add(d)
	for {
		st := m.Status(path)
		if st.State == want {
			return st
		}
		if time.Now().After(end) {
			t.Fatalf("state %v after %v, want %v", st, d, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newMgr(t *testing.T, g workgate.Gate) *Manager {
	t.Helper()
	m := New(Config{Dir: filepath.Join(t.TempDir(), "proxies"), Gate: g, IdleDelay: time.Millisecond})
	t.Cleanup(m.Close)
	return m
}

func TestCandidate(t *testing.T) {
	cases := []struct {
		s    Source
		mode string
		want bool
	}{
		{Source{Codec: "h264", PixFmt: "yuv420p", W: 1280, H: 720}, ModeAuto, false},
		{Source{Codec: "h264", PixFmt: "yuv420p", W: 1920, H: 1080}, ModeAuto, true},
		{Source{Codec: "hevc", PixFmt: "yuv420p", W: 1280, H: 720}, ModeAuto, true},
		{Source{Codec: "h264", PixFmt: "yuv422p10le", W: 1280, H: 720}, ModeAuto, true},
		{Source{Codec: "h264", PixFmt: "yuv420p", W: 1280, H: 720, Bitrate: 40e6}, ModeAuto, true},
		{Source{Codec: "h264", PixFmt: "yuv420p", W: 640, H: 360}, ModeAlways, true},
		{Source{Codec: "h264", PixFmt: "yuv420p", W: 3840, H: 2160}, ModeOff, false},
	}
	for i, c := range cases {
		if got := Candidate(c.s, c.mode); got != c.want {
			t.Errorf("case %d: %v", i, got)
		}
	}
}

func TestGenerateAndLookup(t *testing.T) {
	needFFmpeg(t)
	src := genSource(t, t.TempDir(), 1920, 1080, "2")
	m := newMgr(t, workgate.NopGate{})
	if _, ok := m.Lookup(src); ok {
		t.Fatal("proxy before generation")
	}
	m.Want(src)
	m.Want(src) // idempotent
	st := waitState(t, m, src, StateReady, 60*time.Second)
	if st.Percent != 100 {
		t.Fatalf("%+v", st)
	}
	info, ok := m.Lookup(src)
	if !ok || info.H != 540 || info.W != 960 || info.Frames != 60 {
		t.Fatalf("info %+v ok=%v", info, ok)
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatal(err)
	}
	// All-intra: every packet is a keyframe.
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=flags",
		"-of", "csv=p=0", info.Path).Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Fields(string(out)) {
		if !strings.HasPrefix(l, "K") {
			t.Fatalf("non-keyframe packet %q in proxy", l)
		}
	}
	// no leftovers
	ents, _ := os.ReadDir(filepath.Dir(info.Path))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Fatalf("leftover %s", e.Name())
		}
	}
	// modified source: the proxy no longer matches
	time.Sleep(10 * time.Millisecond)
	if err := os.Chtimes(src, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	m.invalidate(src)
	if _, ok := m.Lookup(src); ok {
		t.Fatal("stale proxy served for changed source")
	}
}

func TestNotACandidateMakesNoProxy(t *testing.T) {
	needFFmpeg(t)
	src := genSource(t, t.TempDir(), 640, 360, "1")
	m := newMgr(t, workgate.NopGate{})
	m.Want(src)
	time.Sleep(600 * time.Millisecond)
	if _, ok := m.Lookup(src); ok {
		t.Fatal("proxy for a small source in auto mode")
	}
	m.SetMode(ModeAlways)
	m.Regenerate(src)
	waitState(t, m, src, StateReady, 30*time.Second)
	m.SetMode(ModeOff)
	if _, ok := m.Lookup(src); ok {
		t.Fatal("proxy served in mode off")
	}
}

func TestCorruptSourceFailsOnce(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "bad.mp4")
	if err := os.WriteFile(src, []byte("not a video at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newMgr(t, workgate.NopGate{})
	m.Want(src)
	waitState(t, m, src, StateFailed, 20*time.Second)
	if _, ok := m.Lookup(src); ok {
		t.Fatal("proxy of corrupt file")
	}
	// no retry until the source changes: Want leaves the failed mark alone
	m.Want(src)
	time.Sleep(200 * time.Millisecond)
	if st := m.Status(src); st.State != StateFailed {
		t.Fatalf("%+v", st)
	}
}

func TestSuspendedWhilePlaying(t *testing.T) {
	needFFmpeg(t)
	src := genSource(t, t.TempDir(), 1920, 1080, "20")
	g := workgate.New()
	defer g.Close()
	m := newMgr(t, g)
	m.Want(src)
	waitState(t, m, src, StateRunning, 20*time.Second)
	g.SetState(workgate.Playing)
	// the ffmpeg child must be stopped and consume no CPU
	var pid string
	end := time.Now().Add(2 * time.Second)
	for time.Now().Before(end) {
		out, _ := exec.Command("pgrep", "-f", "proxies/.*\\.mp4\\.part").Output()
		if f := strings.Fields(string(out)); len(f) > 0 {
			pid = f[0]
			b, _ := os.ReadFile("/proc/" + pid + "/stat")
			s := string(b)
			if i := strings.LastIndexByte(s, ')'); i > 0 && s[i+2] == 'T' {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pid == "" {
		t.Skip("cannot find the ffmpeg child (pgrep missing?)")
	}
	cpu := func() string {
		b, _ := os.ReadFile("/proc/" + pid + "/stat")
		f := strings.Fields(string(b))
		return f[13] + "/" + f[14]
	}
	c0 := cpu()
	time.Sleep(500 * time.Millisecond)
	if c1 := cpu(); c0 != c1 {
		t.Fatalf("stopped ffmpeg used CPU: %s -> %s", c0, c1)
	}
	g.SetState(workgate.Idle)
	waitState(t, m, src, StateReady, 90*time.Second)
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	m := New(Config{Dir: dir, Now: func() time.Time { return now }})
	defer m.Close()
	mk := func(key string, size int, last time.Time, src string, mp4 bool) {
		if mp4 {
			_ = os.WriteFile(filepath.Join(dir, key+".mp4"), make([]byte, size), 0o644)
		}
		_ = writeSidecar(filepath.Join(dir, key+".json"), sidecar{SrcPath: src, LastUsed: last, Version: FormatVersion})
	}
	live := filepath.Join(dir, "live.src")
	_ = os.WriteFile(live, nil, 0o644)
	mk("old", 1000, now.Add(-time.Hour), live, true)
	mk("older", 1000, now.Add(-2*time.Hour), live, true)
	mk("gone", 1000, now.Add(-40*24*time.Hour), "/nonexistent/x.mp4", true)
	mk("gonenew", 1000, now.Add(-time.Hour), "/nonexistent/y.mp4", true)
	mk("nomedia", 0, now, live, false)
	_ = os.WriteFile(filepath.Join(dir, "orphan.mp4"), make([]byte, 10), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "x.mp4.part"), make([]byte, 10), 0o644)
	m.setCapBytes(2000)
	time.Sleep(50 * time.Millisecond)
	m.Cleanup()
	has := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }
	switch {
	case has("orphan.mp4"), has("x.mp4.part"), has("gone.mp4"), has("gone.json"), has("nomedia.json"):
		t.Fatal("leftover not removed")
	case has("older.mp4"):
		t.Fatal("LRU entry over the cap kept")
	case !has("old.mp4") || !has("gonenew.mp4"):
		t.Fatal("recent entries removed")
	}
}

func TestClearAndRemove(t *testing.T) {
	m := newMgr(t, workgate.NopGate{})
	_ = os.MkdirAll(m.dir, 0o755)
	_ = os.WriteFile(filepath.Join(m.dir, "a.mp4"), []byte("x"), 0o644)
	m.Clear()
	if ents, _ := os.ReadDir(m.dir); len(ents) != 0 {
		t.Fatal("not cleared")
	}
}

func TestAlphaSourcesGetNoProxy(t *testing.T) {
	for _, pf := range []string{"yuva444p12le", "rgba", "yuva420p", "gbrap10le", "argb"} {
		for _, mode := range []string{ModeAuto, ModeAlways} {
			if Candidate(Source{Codec: "prores", PixFmt: pf, W: 3840, H: 2160}, mode) {
				t.Errorf("%s/%s: a proxy would drop the alpha channel", pf, mode)
			}
		}
	}
	if !HasAlpha("yuva444p") || HasAlpha("yuv444p") || HasAlpha("yuv420p10le") {
		t.Fatal("HasAlpha")
	}
}

// A proxy over the size cap must not be built, trimmed and built again forever.
func TestCapDoesNotMakeProxiesRebuildEndlessly(t *testing.T) {
	needFFmpeg(t)
	src := genSource(t, t.TempDir(), 1920, 1080, "1")
	probes := 0
	var mu sync.Mutex
	m := New(Config{Dir: filepath.Join(t.TempDir(), "proxies"), Gate: workgate.NopGate{}, IdleDelay: time.Millisecond})
	inner := m.cfg.Probe
	m.cfg.Probe = func(ctx context.Context, p string) (Source, error) {
		mu.Lock()
		probes++
		mu.Unlock()
		return inner(ctx, p)
	}
	defer m.Close()
	m.setCapBytes(1000) // far below one proxy
	m.Want(src)
	waitState(t, m, src, StateReady, 60*time.Second)
	// playback keeps asking for it; the cap is exceeded, but no encode may restart
	end := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(end) {
		m.Lookup(src)
		m.Want(src)
		time.Sleep(5 * time.Millisecond)
	}
	m.Cleanup()
	mu.Lock()
	defer mu.Unlock()
	if probes != 1 {
		t.Fatalf("source probed/encoded %d times", probes)
	}
	if _, ok := m.Lookup(src); !ok {
		t.Fatal("the proxy in use was trimmed")
	}
}
