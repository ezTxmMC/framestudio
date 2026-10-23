package edit

import "testing"

func TestCacheBarEmptyAtStart(t *testing.T) {
	f := newFixture(t, basicProject())
	if f.pg.updateCacheBar() {
		t.Fatal("an empty cache must not draw a bar")
	}
}
