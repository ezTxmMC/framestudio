package application

import (
	"runtime/debug"
	"testing"
)

type fakeBudget struct{ shares []float64 }

func (f *fakeBudget) ScaleBudget(s float64) { f.shares = append(f.shares, s) }

func TestMemoryLimit(t *testing.T) {
	const gb = int64(1) << 30
	cases := []struct{ ram, want int64 }{
		{0, 3 * gb}, // unknown: 8 GB machine -> 4 GB half -> capped at 3
		{2 * gb, 3 * gb / 2},
		{4 * gb, 2 * gb},
		{6 * gb, 3 * gb},
		{60 * gb, 3 * gb},
	}
	for _, c := range cases {
		if got := MemoryLimit(c.ram); got != c.want {
			t.Errorf("ram %d: limit %d, want %d", c.ram, got, c.want)
		}
	}
}

func TestParseMemTotal(t *testing.T) {
	if got := ParseMemTotal([]byte("MemTotal:       16384000 kB\nMemFree: 1 kB\n")); got != 16384000<<10 {
		t.Fatal(got)
	}
	for _, bad := range []string{"", "MemFree: 5 kB", "MemTotal: x kB", "MemTotal:"} {
		if ParseMemTotal([]byte(bad)) != 0 {
			t.Fatalf("%q", bad)
		}
	}
}

func TestAutoCacheMB(t *testing.T) {
	const gb = int64(1) << 30
	cases := []struct {
		set  int
		ram  int64
		want int
	}{
		{-1, 16 * gb, 0}, {512, 16 * gb, 512}, {0, 16 * gb, 1638}, {0, gb, 256}, {0, 60 * gb, 2048}, {0, 0, 819},
	}
	for _, c := range cases {
		if got := AutoCacheMB(c.set, c.ram); got != c.want {
			t.Errorf("%d/%d: %d, want %d", c.set, c.ram, got, c.want)
		}
	}
}

func TestTuneGCRespectsEnvironment(t *testing.T) {
	old := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(old)
	if got := TuneGC(func(k string) string {
		if k == "GOMEMLIMIT" {
			return "1GiB"
		}
		return ""
	}, 16<<30); got != -1 {
		t.Fatalf("user GOMEMLIMIT overridden: %d", got)
	}
	if got := TuneGC(func(string) string { return "" }, 16<<30); got != 3<<30 {
		t.Fatalf("limit %d", got)
	}
	if debug.SetMemoryLimit(-1) != 3<<30 {
		t.Fatal("limit not set")
	}
}

func TestMemoryWatcherHysteresis(t *testing.T) {
	heap := int64(0)
	w := NewMemoryWatcher(1000, func() int64 { return heap })
	b := &fakeBudget{}
	w.Add(b)
	for _, step := range []struct {
		heap  int64
		calls int
		press bool
	}{
		{500, 0, false}, {801, 1, true}, {700, 1, true}, {601, 1, true}, {599, 2, false}, {700, 2, false},
	} {
		heap = step.heap
		w.Check()
		if len(b.shares) != step.calls || w.Pressure() != step.press {
			t.Fatalf("heap %d: calls %v pressure %v", step.heap, b.shares, w.Pressure())
		}
	}
	if b.shares[0] != 0.5 || b.shares[1] != 1 {
		t.Fatalf("%v", b.shares)
	}
}
