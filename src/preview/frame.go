package preview

import (
	"image"
	"sync"
	"sync/atomic"
)

// Quality is a step of the resolution ladder.
type Quality int

const (
	QFull Quality = iota
	QHalf
	QQuarter
)

func (q Quality) String() string {
	switch q {
	case QHalf:
		return "Halb"
	case QQuarter:
		return "Viertel"
	}
	return "Voll"
}

// Source tells where the pixels of a frame came from.
type Source int

const (
	SrcSW Source = iota
	SrcHW
	SrcProxy
	SrcCache
)

func (s Source) String() string {
	switch s {
	case SrcHW:
		return "hw"
	case SrcProxy:
		return "proxy"
	case SrcCache:
		return "cache"
	}
	return "sw"
}

// minRenderW is the narrowest picture the ladder renders.
const minRenderW = 160

// SizeFor divides the target by the quality step: even dimensions, at least
// minRenderW wide, never larger than the target.
func SizeFor(t Target, q Quality) (w, h int) {
	if t.Empty() {
		return 0, 0
	}
	div := 1 << uint(q)
	w, h = t.W/div, t.H/div
	if w < minRenderW {
		k := float64(minRenderW) / float64(t.W)
		w, h = minRenderW, int(float64(t.H)*k+0.5)
	}
	if q != QFull {
		w, h = w&^1, h&^1
	}
	w, h = min(max(w, 1), t.W), min(max(h, 1), t.H)
	return w, h
}

// Frame is one rendered preview picture. It is reference counted: the scheduler, the
// cache and the viewer each hold a reference while they use the pixels, and the
// buffer goes back to its pool when the last one is released. Pixels are immutable
// once the frame is published.
type Frame struct {
	Img      *image.RGBA
	Seq      uint64
	Pos      int    // timeline frame
	Gen      uint64 // project revision the frame was rendered from
	Q        Quality
	Src      Source
	Late     bool
	Approx   bool // stand-in for a different position
	RenderNS int64

	nrgba atomic.Pointer[image.NRGBA]
	ref   atomic.Int32
	pool  *BufPool
}

// Retain adds a reference.
func (f *Frame) Retain() *Frame {
	f.ref.Add(1)
	return f
}

// Release drops a reference; the last one recycles the buffer. nil is allowed.
func (f *Frame) Release() {
	if f == nil {
		return
	}
	switch n := f.ref.Add(-1); {
	case n == 0:
		f.nrgba.Store(nil)
		if f.pool != nil {
			f.pool.put(f.Img)
		}
	case n < 0:
		panic("preview: frame released too often")
	}
}

// Bytes is the pixel memory of the frame.
func (f *Frame) Bytes() int64 { return int64(len(f.Img.Pix)) }

// NRGBA returns the frame as a straight-alpha image for consumers like the scopes. It
// is computed once on first use; call it off the UI thread.
func (f *Frame) NRGBA() *image.NRGBA {
	if n := f.nrgba.Load(); n != nil {
		return n
	}
	b := f.Img.Rect
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		s := f.Img.Pix[y*f.Img.Stride : y*f.Img.Stride+b.Dx()*4]
		d := n.Pix[y*n.Stride : y*n.Stride+b.Dx()*4]
		for i := 0; i+3 < len(s); i += 4 {
			a := uint32(s[i+3])
			switch a {
			case 255:
				copy(d[i:i+4:i+4], s[i:i+4:i+4])
			case 0:
			default:
				d[i] = uint8(min(255, (uint32(s[i])*255+a/2)/a))
				d[i+1] = uint8(min(255, (uint32(s[i+1])*255+a/2)/a))
				d[i+2] = uint8(min(255, (uint32(s[i+2])*255+a/2)/a))
				d[i+3] = uint8(a)
			}
		}
	}
	f.nrgba.Store(n)
	return n
}

// BufPool recycles RGBA buffers of one size. It keeps at most maxFree idle buffers so a
// burst (a cache that was just evicted) does not pin memory.
type BufPool struct {
	w, h    int
	mu      sync.Mutex
	free    []*image.RGBA
	maxFree int
	allocs  atomic.Int64
}

// NewBufPool makes a pool of w x h buffers keeping up to maxFree idle ones.
func NewBufPool(w, h, maxFree int) *BufPool {
	return &BufPool{w: w, h: h, maxFree: maxFree}
}

// Size returns the buffer size.
func (p *BufPool) Size() (int, int) { return p.w, p.h }

// Allocs counts buffers that had to be allocated (pool misses).
func (p *BufPool) Allocs() int64 { return p.allocs.Load() }

// Get returns a frame (one reference) with an undefined-content buffer.
func (p *BufPool) Get() *Frame {
	var img *image.RGBA
	p.mu.Lock()
	if n := len(p.free); n > 0 {
		img = p.free[n-1]
		p.free[n-1] = nil
		p.free = p.free[:n-1]
	}
	p.mu.Unlock()
	if img == nil {
		img = image.NewRGBA(image.Rect(0, 0, p.w, p.h))
		p.allocs.Add(1)
	}
	f := &Frame{Img: img, pool: p}
	f.ref.Store(1)
	return f
}

func (p *BufPool) put(img *image.RGBA) {
	p.mu.Lock()
	if len(p.free) < p.maxFree {
		p.free = append(p.free, img)
	}
	p.mu.Unlock()
}
