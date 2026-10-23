package shell

import (
	"testing"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/store"
)

// resetSettings gives the test factory settings (kept in memory: settings.Load is never
// called in tests) and restores them afterwards.
func resetSettings(t *testing.T) {
	t.Helper()
	reset := func() {
		_ = settings.Update(func(s *settings.Settings) { *s = settings.Defaults() })
		store.SetProjectsDir("")
	}
	reset()
	t.Cleanup(reset)
}
