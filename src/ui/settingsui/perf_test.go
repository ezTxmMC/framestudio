package settingsui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
)

func TestPerformanceCategoryControls(t *testing.T) {
	f := newFixture(t, "perf")
	if f.w.cur.title != "Vorschau & Leistung" {
		t.Fatalf("category %q", f.w.cur.title)
	}
	cache := f.field("Vorschau-Cache").input.(*widget.Select)
	if cache.Selected != "Automatisch" {
		t.Fatalf("default cache %q", cache.Selected)
	}
	cache.SetSelected("1 GB")
	if settings.Get().Playback.CacheMB != 1024 {
		t.Fatal("cache size not applied")
	}
	cache.SetSelected("Aus")
	if settings.Get().Playback.CacheMB != -1 {
		t.Fatal("cache off")
	}

	proxy := f.field("Proxys").input.(*widget.Select)
	proxy.SetSelected("Aus")
	if settings.Get().Playback.ProxyMode != "off" {
		t.Fatal("proxy mode")
	}
	hw := f.field("Hardware-Dekodierung").input.(*widget.Select)
	hw.SetSelected("Aus")
	if settings.Get().Playback.HwDecode != "off" {
		t.Fatal("hw decode")
	}
	idle := f.field("Im Leerlauf vorrendern").input.(*widget.Check)
	if !idle.Checked {
		t.Fatal("background rendering is on by default")
	}
	test.Tap(idle)
	if settings.Get().Playback.BackgroundCache {
		t.Fatal("background cache switch")
	}
	stats := f.field("Wiedergabe-Statistik anzeigen").input.(*widget.Check)
	if stats.Checked {
		t.Fatal("statistics are off by default")
	}
	test.Tap(stats)
	if !settings.Get().Playback.Stats || !f.saved().Playback.Stats {
		t.Fatal("statistics switch must apply and persist")
	}
}
