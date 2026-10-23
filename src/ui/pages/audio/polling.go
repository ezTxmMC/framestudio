package audio

import (
	"sync"
	"sync/atomic"
	"time"

	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/liveaudio"
)

var zeroLevel liveaudio.Level

// meterRate is how often the meters poll the live levels.
const meterRate = 30

// tickerClock calls tick on the UI thread meterRate times a second until stop is called.
// A tick is skipped while the previous one is still queued, so a busy UI thread does not
// pile up work.
func tickerClock(tick func()) (stop func()) {
	done := make(chan struct{})
	var pending atomic.Bool
	go func() {
		t := time.NewTicker(time.Second / meterRate)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if pending.CompareAndSwap(false, true) {
					kit.Do(func() {
						pending.Store(false)
						tick()
					})
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// metersWanted reports whether the meters have to poll: while sound plays, and until
// they have fallen back to rest afterwards.
func (p *Page) metersWanted() bool {
	if p.content == nil || p.closed || !p.active {
		return false
	}
	return p.s.Playing || p.prev.Busy() || !p.mixer.resting()
}

// syncMeters starts or stops polling to match metersWanted.
func (p *Page) syncMeters() {
	switch want := p.metersWanted(); {
	case want && p.meterStop == nil:
		p.meterLast = p.now()
		p.meterGen++
		gen := p.meterGen
		p.meterStop = p.clock(func() {
			if gen == p.meterGen && p.meterStop != nil && !p.closed {
				p.meterTick()
			}
		})
	case !want && p.meterStop != nil:
		p.stopMeters()
	}
}

func (p *Page) stopMeters() {
	if p.meterStop != nil {
		p.meterGen++
		p.meterStop()
		p.meterStop = nil
	}
}

// meterTick reads the live levels and advances the meter ballistics.
func (p *Page) meterTick() {
	now := p.now()
	dt := now.Sub(p.meterLast)
	p.meterLast = now
	if dt <= 0 || dt > time.Second {
		dt = time.Second / meterRate
	}
	sounding := p.s.Playing || p.prev.Busy()
	master, tracks := p.levels(p.s)
	if !sounding {
		master, tracks = zeroLevel, nil // decay after stop, whatever the engine still reports
	}
	p.mixer.stepMeters(master, tracks, dt)
	if !sounding && p.mixer.resting() {
		p.stopMeters()
	}
}
