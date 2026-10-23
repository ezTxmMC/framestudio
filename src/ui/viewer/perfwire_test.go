package viewer

import (
	"testing"

	"framestudio-proto/src/settings"
)

// The 2 GB and 4 GB choices of the preview cache really give a bigger cache.
func TestCacheBytesHonoursLargeChoices(t *testing.T) {
	var s settings.Settings
	for _, c := range []struct {
		mb   int
		want int64
	}{{512, 512 << 20 * 2 / 3}, {2048, 2048 << 20 * 2 / 3}, {4096, 4096 << 20 * 2 / 3}, {-1, 0}} {
		s.Playback.CacheMB = c.mb
		if got := cacheBytes(s); got != c.want {
			t.Errorf("CacheMB %d: %d bytes, want %d", c.mb, got, c.want)
		}
	}
	s.Playback.CacheMB = 0
	if got := cacheBytes(s); got > 1<<30 {
		t.Errorf("automatic cache %d exceeds 1 GiB", got)
	}
}
