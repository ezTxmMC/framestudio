package preview

import (
	"math"
	"sync/atomic"
)

// Clock publishes the playhead for lock-free readers (audio, scheduler, stats). The UI
// thread writes it whenever the session playhead moves; readers never block it and
// never feed anything back: the picture does not influence the clock.
type Clock struct {
	frame   atomic.Int64
	speed   atomic.Uint64 // float64 bits
	playing atomic.Bool
	epoch   atomic.Uint64
}

// Set publishes the playhead. Epoch increases on every discontinuity (seek, start, stop).
func (c *Clock) Set(frame int, playing bool, speed float64, discontinuity bool) {
	c.frame.Store(int64(frame))
	c.speed.Store(math.Float64bits(speed))
	c.playing.Store(playing)
	if discontinuity {
		c.epoch.Add(1)
	}
}

// Now returns the playhead frame and the epoch.
func (c *Clock) Now() (frame int, epoch uint64) { return int(c.frame.Load()), c.epoch.Load() }

// Speed is the playback rate.
func (c *Clock) Speed() float64 { return math.Float64frombits(c.speed.Load()) }

// Playing reports whether the clock runs.
func (c *Clock) Playing() bool { return c.playing.Load() }
