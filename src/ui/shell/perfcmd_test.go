package shell

import (
	"testing"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/statsui"
)

func TestPreviewStatsCommandToggleAndPersist(t *testing.T) {
	sh, _, _ := newTestShell(t)
	c, ok := sh.reg.Get("view.previewStats")
	if !ok {
		t.Fatal("view.previewStats is not registered")
	}
	if c.Label != "Wiedergabe-Statistik" || c.Checked {
		t.Fatalf("label %q checked %v", c.Label, c.Checked)
	}
	if c.Shortcut != "" {
		t.Fatalf("no default shortcut, got %q", c.Shortcut)
	}
	sh.reg.Run("view.previewStats")
	if !settings.Get().Playback.Stats {
		t.Fatal("the command did not switch the setting on")
	}
	if c, _ = sh.reg.Get("view.previewStats"); !c.Checked {
		t.Fatal("menu check mark follows the setting")
	}
	sh.reg.Run("view.previewStats")
	if settings.Get().Playback.Stats {
		t.Fatal("second run switches it off")
	}
}

func TestPerformanceCommandsFollowTheirHooks(t *testing.T) {
	sh, _, _ := newTestShell(t)
	statsui.SetHooks(statsui.Hooks{})
	defer statsui.SetHooks(statsui.Hooks{})
	for _, id := range []string{"cache.clear", "media.proxyClear"} {
		c, ok := sh.reg.Get(id)
		if !ok || c.Enabled {
			t.Fatalf("%s without a hook must be disabled (found %v, enabled %v)", id, ok, c.Enabled)
		}
	}
	cleared, proxyCleared := 0, 0
	statsui.SetHooks(statsui.Hooks{ClearCache: func() { cleared++ }, ProxyClear: func() { proxyCleared++ }})
	sh.reg.Run("cache.clear")
	sh.reg.Run("media.proxyClear")
	if cleared != 1 || proxyCleared != 1 {
		t.Fatalf("hooks ran %d/%d times", cleared, proxyCleared)
	}
}
