package edit

import (
	"testing"

	"framestudio-proto/src/workgate"
)

func TestCacheBarEmptyAtStart(t *testing.T) {
	f := newFixture(t, basicProject())
	if f.pg.updateCacheBar() {
		t.Fatal("an empty cache must not draw a bar")
	}
}

func TestCacheBarPollSkippedWhileScrubbing(t *testing.T) {
	f := newFixture(t, basicProject())
	g := workgate.New()
	old := workgate.Default()
	workgate.SetDefault(g)
	defer func() { workgate.SetDefault(old); g.Close() }()
	if !f.pg.cacheBarDue() {
		t.Fatal("idle: the bar may update")
	}
	g.SetState(workgate.Scrubbing)
	if f.pg.cacheBarDue() {
		t.Fatal("scrubbing: the cache scan must wait")
	}
}
