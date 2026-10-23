package media

import (
	"sync"
	"sync/atomic"
)

// BufPool recycles the big byte slabs of decoded frames (RGBA, w*h*4) by exact size, so
// steady playback allocates nothing per frame. The free lists are capped in bytes.
type BufPool struct {
	mu     sync.Mutex
	free   map[int][][]byte
	pooled int64
	limit  int64

	gets, reuses atomic.Uint64
}

// NewBufPool creates a pool that keeps at most limit bytes of idle buffers.
func NewBufPool(limit int64) *BufPool {
	return &BufPool{free: map[int][][]byte{}, limit: limit}
}

// Get returns a buffer of exactly size bytes. Its content is undefined.
func (p *BufPool) Get(size int) []byte {
	p.gets.Add(1)
	p.mu.Lock()
	if l := p.free[size]; len(l) > 0 {
		b := l[len(l)-1]
		l[len(l)-1] = nil
		p.free[size] = l[:len(l)-1]
		p.pooled -= int64(size)
		p.mu.Unlock()
		p.reuses.Add(1)
		return b
	}
	p.mu.Unlock()
	return make([]byte, size)
}

// Put hands a buffer back. The caller must not touch it afterwards.
func (p *BufPool) Put(b []byte) {
	size := len(b)
	if size == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pooled+int64(size) > p.limit {
		return // let the GC have it
	}
	p.free[size] = append(p.free[size], b)
	p.pooled += int64(size)
}

// SetLimit changes the idle byte cap and trims the free lists to it.
func (p *BufPool) SetLimit(limit int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.limit = limit
	for size, l := range p.free {
		for len(l) > 0 && p.pooled > p.limit {
			l[len(l)-1] = nil
			l = l[:len(l)-1]
			p.pooled -= int64(size)
		}
		p.free[size] = l
	}
}

// Stats reports requests, how many were served from the free lists, and idle bytes.
func (p *BufPool) Stats() (gets, reuses uint64, idleBytes int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gets.Load(), p.reuses.Load(), p.pooled
}

// pframe is a pooled frame buffer with a reference count. The creator holds the first
// reference, the frame cache one more while it stores the frame, and every reader of
// VideoFrameInto one while it looks at the pixels. The buffer returns to the pool when
// the count reaches zero, unless it escaped: handed out by the plain VideoFrame API,
// whose callers keep images for as long as they like.
type pframe struct {
	buf     []byte
	pool    *BufPool
	refs    atomic.Int32
	escaped atomic.Bool
}

func (p *BufPool) newFrame(size int) *pframe {
	pf := &pframe{buf: p.Get(size), pool: p}
	pf.refs.Store(1)
	return pf
}

func (f *pframe) release() {
	if f.refs.Add(-1) == 0 && !f.escaped.Load() {
		f.pool.Put(f.buf)
	}
}
