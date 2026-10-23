package audio

import (
	"math"
	"time"
)

// Level is the recent output level of a track or the master bus, linear 0..1 per
// channel (L, R). Peak decays over ~300 ms; RMS covers ~50 ms.
type Level struct {
	Peak, RMS [2]float32
	Clipped   bool // true for ~1 s after a sample reached full scale
}

const (
	ringSize   = 256 // 2.56 s of blocks
	peakDecay  = 300 * time.Millisecond
	rmsWindow  = 50 * time.Millisecond
	clipHold   = time.Second
	levelSpan  = 2 * time.Second // older blocks no longer matter
	blockSpan  = time.Second * BlockFrames / SampleRate
	clipLevel  = 0.999
	silentPeak = 1e-6
)

// meter sums one block of one bus.
type meter struct {
	peak, sum [2]float32
	n         int
	clip      bool
}

func (m *meter) add(buf []float32, n int, limit bool) {
	*m = meter{n: n}
	for i := range n {
		for c := range 2 {
			x := buf[2*i+c]
			if x >= clipLevel || x <= -clipLevel {
				m.clip = true
			}
			if limit {
				x = SoftClip(x)
			}
			if x < 0 {
				x = -x
			}
			m.peak[c] = max(m.peak[c], x)
			m.sum[c] += x * x
		}
	}
}

// blockLevel is the metering of one block, stamped with the time it is heard.
type blockLevel struct {
	at     time.Time
	master meter
	ids    []string
	tracks []meter
}

// record stores the levels of the block just rendered (post-fader per track, master
// as sent to the output).
func (v *Voice) record(heard time.Time, n int, plan *Plan) {
	v.lmu.Lock()
	defer v.lmu.Unlock()
	b := &v.ring[v.ringN%ringSize]
	v.ringN++
	b.at = heard
	b.master.add(v.buf, n, true)
	b.ids = b.ids[:0]
	b.tracks = b.tracks[:0]
	if plan != nil {
		for i, t := range plan.Tracks {
			var m meter
			m.add(v.trk[i], n, false)
			b.ids = append(b.ids, t.ID)
			b.tracks = append(b.tracks, m)
		}
	}
}

// Levels returns the meters as heard at time now: master and every audio track of the
// current plan (by track ID). Zero when nothing sounded recently.
func (v *Voice) Levels(now time.Time) (master Level, tracks map[string]Level) {
	v.lmu.Lock()
	defer v.lmu.Unlock()
	tracks = make(map[string]Level, len(v.ids))
	for _, id := range v.ids {
		tracks[id] = Level{}
	}
	type acc struct {
		lv  Level
		sum [2]float32
		n   int
	}
	var m acc
	ta := map[string]*acc{}
	apply := func(a *acc, b *blockLevel, mt *meter) {
		age := now.Sub(b.at.Add(blockSpan))
		decay := float32(1)
		if age > 0 {
			decay = float32(math.Exp(-age.Seconds() / peakDecay.Seconds()))
		}
		for c := range 2 {
			a.lv.Peak[c] = max(a.lv.Peak[c], mt.peak[c]*decay)
		}
		if b.at.Add(blockSpan).After(now.Add(-rmsWindow)) {
			a.sum[0] += mt.sum[0]
			a.sum[1] += mt.sum[1]
			a.n += mt.n
		}
		if mt.clip && now.Sub(b.at) <= clipHold {
			a.lv.Clipped = true
		}
	}
	for i := range min(v.ringN, ringSize) {
		b := &v.ring[i]
		if b.at.After(now) || now.Sub(b.at) > levelSpan {
			continue // not heard yet, or long gone
		}
		apply(&m, b, &b.master)
		for ti, id := range b.ids {
			if _, ok := tracks[id]; !ok {
				continue
			}
			a := ta[id]
			if a == nil {
				a = &acc{}
				ta[id] = a
			}
			apply(a, b, &b.tracks[ti])
		}
	}
	finish := func(a *acc) Level {
		if a.n > 0 {
			for c := range 2 {
				a.lv.RMS[c] = float32(math.Sqrt(float64(a.sum[c]) / float64(a.n)))
			}
		}
		for c := range 2 {
			if a.lv.Peak[c] < silentPeak {
				a.lv.Peak[c] = 0
			}
		}
		return a.lv
	}
	for id, a := range ta {
		tracks[id] = finish(a)
	}
	return finish(&m), tracks
}
