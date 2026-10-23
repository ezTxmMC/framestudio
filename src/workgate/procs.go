package workgate

import "sync"

// proc is a registered child process (its own process group).
type proc struct {
	pid     int
	prio    Prio
	stopped bool
}

type procSet struct {
	mu sync.Mutex
	m  map[*proc]struct{}
}

func (s *procSet) add(pid int, p Prio) *proc {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[*proc]struct{}{}
	}
	pr := &proc{pid: pid, prio: p}
	s.m[pr] = struct{}{}
	return pr
}

func (s *procSet) remove(p *proc) {
	s.mu.Lock()
	delete(s.m, p)
	s.mu.Unlock()
}

// wantStopped says whether a process of priority p must be stopped in state st.
func wantStopped(p Prio, st State, allowVis bool) bool {
	switch st {
	case Playing:
		return p == PrioBackground || (p == PrioVisible && !allowVis)
	case Scrubbing:
		return p == PrioBackground
	}
	return false
}

func (s *procSet) update(st State, allowVis bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.m {
		if want := wantStopped(p.prio, st, allowVis); want != p.stopped {
			if !signalGroup(p.pid, want) {
				delete(s.m, p) // gone
				continue
			}
			p.stopped = want
		}
	}
}

func (s *procSet) resumeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.m {
		if p.stopped {
			signalGroup(p.pid, false)
			p.stopped = false
		}
	}
}

func (s *procSet) stopped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for p := range s.m {
		if p.stopped {
			n++
		}
	}
	return n
}
