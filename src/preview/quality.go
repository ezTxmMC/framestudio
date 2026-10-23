package preview

import (
	"framestudio-proto/src/model"
	"slices"
	"time"
)

// Sample is what the controller learns from one rendered frame.
type Sample struct {
	At       time.Time
	Period   time.Duration // one frame period of the project
	RenderNS int64         // render time including decode waits
	Late     bool          // the frame finished more than a period behind the clock
	UIBusy   bool          // the LoadGovernor reports a congested UI thread
	Playing  bool
	Speed    float64
	// Cold marks the first frame of a run or at a new size: it pays for starting
	// decoders, which says nothing about the steady-state cost, so it is not sampled.
	Cold bool
}

const (
	qcWindow      = 30
	qcMinSamples  = 8
	qcDownP90     = 0.80
	qcDownLate    = 0.15
	qcEmergency   = 1.5
	qcUpP90       = 0.45
	qcUpPredictK  = 3.2
	qcUpPredictLT = 0.70
	qcMinDecision = 500 * time.Millisecond
	qcBaseDwell   = time.Second
	qcMaxDwell    = 8 * time.Second
	qcFlapWindow  = 5 * time.Second
	qcResetStable = 30 * time.Second
	// qcWarm is how long the decoders of the next higher step are warmed up before the
	// step is taken (a cold 4K decoder needs several hundred milliseconds).
	qcWarm = 600 * time.Millisecond
	// qcSettle is how long after a cold frame (run start, new size) samples are ignored.
	qcSettle = time.Second
)

// QualityController steps the preview resolution down when the render budget (one
// frame period) is exceeded and back up when there is clear headroom. It is not safe
// for concurrent use; the scheduler goroutine owns it.
type QualityController struct {
	level      Quality
	costs      [qcWindow]int64
	lates      [qcWindow]bool
	n, head    int
	ema        float64
	consecLate int

	lastChange time.Time
	lastUp     time.Time
	dwell      time.Duration

	// underrun tracking
	under      bool
	underSince time.Time
	okSince    time.Time
	halfRate   bool

	changes int

	settleUntil time.Time

	// warm is called when a step up is due, then the step waits qcWarm so the media
	// layer can start its decoders at the new size.
	warm   func(Quality)
	warmAt time.Time
}

// SetWarm installs the callback that prepares the decoders for a coming step up.
func (q *QualityController) SetWarm(f func(Quality)) { q.warm = f }

// NewQualityController starts at full quality.
func NewQualityController() *QualityController {
	return &QualityController{dwell: qcBaseDwell}
}

// Level is the current automatic step.
func (q *QualityController) Level() Quality { return q.level }

// Changes counts level changes so far (tests and flapping checks).
func (q *QualityController) Changes() int { return q.changes }

// UnderRun reports that even the lowest step misses its deadlines.
func (q *QualityController) UnderRun() bool { return q.under }

// HalfRate reports that the scheduler should render every second frame.
func (q *QualityController) HalfRate() bool { return q.halfRate }

// Dwell is the current headroom time required before stepping up.
func (q *QualityController) Dwell() time.Duration { return q.dwell }

func (q *QualityController) reset() {
	q.n, q.head, q.ema, q.consecLate = 0, 0, 0, 0
}

func (q *QualityController) p90() float64 {
	if q.n == 0 {
		return 0
	}
	v := make([]int64, 0, qcWindow) // stack-sized; the controller runs once per frame
	v = append(v, q.costs[:q.n]...)
	slices.Sort(v)
	return float64(v[min(q.n-1, q.n*9/10)])
}

func (q *QualityController) lateRate() float64 {
	if q.n == 0 {
		return 0
	}
	c := 0
	for _, l := range q.lates[:q.n] {
		if l {
			c++
		}
	}
	return float64(c) / float64(q.n)
}

func (q *QualityController) step(at time.Time, to Quality) {
	if to == q.level {
		return
	}
	if to < q.level { // up
		q.lastUp = at
	} else if !q.lastUp.IsZero() && at.Sub(q.lastUp) < qcFlapWindow {
		q.dwell = min(q.dwell*2, qcMaxDwell) // the step up was undone: wait longer next time
	}
	q.level = to
	q.warmAt = time.Time{}
	q.lastChange = at
	q.changes++
	q.reset()
}

// Observe feeds one rendered frame and returns the quality for the next one.
func (q *QualityController) Observe(s Sample) Quality {
	if !s.Playing || s.Period <= 0 {
		return q.effective(s)
	}
	period := float64(s.Period)
	if q.lastChange.IsZero() {
		q.lastChange = s.At
	}
	if s.Cold {
		q.settleUntil = s.At.Add(qcSettle)
	}
	if s.At.Before(q.settleUntil) {
		return q.effective(s) // decoders are starting: costs now are not steady-state costs
	}
	q.costs[q.head] = s.RenderNS
	q.lates[q.head] = s.Late
	q.head = (q.head + 1) % qcWindow
	q.n = min(q.n+1, qcWindow)
	if q.ema == 0 {
		q.ema = float64(s.RenderNS)
	} else {
		q.ema += 0.2 * (float64(s.RenderNS) - q.ema)
	}
	if s.Late {
		q.consecLate++
	} else {
		q.consecLate = 0
	}

	if !q.lastUp.IsZero() && s.At.Sub(q.lastChange) > qcResetStable {
		q.dwell = qcBaseDwell
	}

	switch {
	case q.level < QQuarter && (q.ema > qcEmergency*period || q.consecLate >= 3):
		q.step(s.At, q.level+1)
	case q.level < QQuarter && q.n >= qcMinSamples && s.At.Sub(q.lastChange) >= qcMinDecision &&
		(q.p90() > qcDownP90*period || q.lateRate() > qcDownLate):
		q.step(s.At, q.level+1)
	case q.level < QQuarter && s.UIBusy && s.At.Sub(q.lastChange) >= qcMinDecision:
		q.step(s.At, q.level+1)
	case q.level > QFull && !s.UIBusy && q.n >= qcWindow && s.At.Sub(q.lastChange) >= q.dwell:
		p := q.p90()
		if p < qcUpP90*period && q.lateRate() == 0 && p*qcUpPredictK < qcUpPredictLT*period {
			switch {
			case q.warm == nil:
				q.step(s.At, q.level-1)
			case q.warmAt.IsZero():
				q.warmAt = s.At
				q.warm(q.level - 1)
			case s.At.Sub(q.warmAt) >= qcWarm:
				q.step(s.At, q.level-1)
			}
		} else {
			q.warmAt = time.Time{}
		}
	}
	q.trackUnderRun(s)
	return q.effective(s)
}

// effective applies the speed policy: shuttle faster than 2x never uses full size.
func (q *QualityController) effective(s Sample) Quality {
	l := q.level
	if s.Speed > 2 || s.Speed < -2 {
		l = max(l, QHalf)
	}
	return l
}

func (q *QualityController) trackUnderRun(s Sample) {
	bad := q.level == QQuarter && q.n >= qcMinSamples && q.lateRate() > 0.4
	if bad {
		q.okSince = time.Time{}
		if q.underSince.IsZero() {
			q.underSince = s.At
		}
		if s.At.Sub(q.underSince) >= time.Second {
			q.under = true
			q.halfRate = true
		}
		return
	}
	q.underSince = time.Time{}
	if q.under || q.halfRate {
		if q.okSince.IsZero() {
			q.okSince = s.At
		}
		if s.At.Sub(q.okSince) >= 2*time.Second {
			q.under, q.halfRate = false, false
		}
	}
}

// Reset forgets the history (project change, new playback run); the level is kept.
func (q *QualityController) Reset() {
	q.reset()
	q.consecLate = 0
	q.underSince, q.okSince = time.Time{}, time.Time{}
	q.under, q.halfRate = false, false
	q.warmAt = time.Time{}
	q.settleUntil = time.Time{}
}

// Prime sets the first step from a guess of the load before any frame has been timed:
// only while the controller has no history and sits at full quality.
func (q *QualityController) Prime(to Quality) {
	if q.changes == 0 && q.n == 0 && q.level == QFull && to > QFull {
		q.level = to
	}
}

// StartLevel guesses the first ladder step of playback from the video that has to be
// decoded per frame at pos: the sum of the source pictures of all visible layers. One
// 4K clip is fine at full size (measured), several of them are not.
func StartLevel(p *model.Project, pos int) Quality {
	if p == nil {
		return QFull
	}
	var mp float64
	for i := range p.Tracks {
		t := &p.Tracks[i]
		if t.Kind != model.TrackVideo || t.Hidden {
			continue
		}
		ci := t.ClipAt(pos)
		if ci < 0 || t.Clips[ci].Kind != model.KindVideo {
			continue
		}
		if m, ok := p.MediaByID(t.Clips[ci].MediaID); ok {
			mp += float64(m.Width*m.Height) / 1e6
		}
	}
	switch {
	case mp > 40:
		return QQuarter
	case mp > 16:
		return QHalf
	}
	return QFull
}
