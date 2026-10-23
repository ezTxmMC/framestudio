package render

import (
	"image"
	"image/color"
	"reflect"
	"runtime"
	"sync"
)

// layerPool recycles full-frame scratch images between frames. It is a small bounded
// free list rather than a sync.Pool: a sync.Pool is per-P and a render goroutine that
// migrates between Ps would miss its own buffers, allocating 8 MB frames again.
var layerPool struct {
	mu   sync.Mutex
	free []*image.NRGBA
}

const (
	maxFreeLayers = 8
	maxFreeBytes  = 160 << 20
)

func takeLayer(n int) *image.NRGBA {
	layerPool.mu.Lock()
	defer layerPool.mu.Unlock()
	best := -1
	for i, v := range layerPool.free {
		if cap(v.Pix) >= n && (best < 0 || cap(v.Pix) < cap(layerPool.free[best].Pix)) {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	v := layerPool.free[best]
	last := len(layerPool.free) - 1
	layerPool.free[best] = layerPool.free[last]
	layerPool.free[last] = nil
	layerPool.free = layerPool.free[:last]
	return v
}

// getLayer returns a cleared, transparent w x h layer.
func getLayer(w, h int) *image.NRGBA {
	l := getScratch(w, h)
	clear(l.Pix)
	return l
}

// getScratch returns a w x h image with undefined content (the caller overwrites all of it).
func getScratch(w, h int) *image.NRGBA {
	n := w * h * 4
	if v := takeLayer(n); v != nil {
		v.Pix = v.Pix[:n]
		v.Stride = w * 4
		v.Rect = image.Rect(0, 0, w, h)
		return v
	}
	return image.NewNRGBA(image.Rect(0, 0, w, h))
}

func putLayer(l *image.NRGBA) {
	layerPool.mu.Lock()
	defer layerPool.mu.Unlock()
	held, smallest := 0, -1
	for i, v := range layerPool.free {
		held += cap(v.Pix)
		if smallest < 0 || cap(v.Pix) < cap(layerPool.free[smallest].Pix) {
			smallest = i
		}
	}
	if len(layerPool.free) >= maxFreeLayers {
		// make room by dropping the smallest buffer if the new one is bigger (the
		// frame size changed: old sizes are useless)
		if cap(layerPool.free[smallest].Pix) >= cap(l.Pix) {
			return
		}
		held -= cap(layerPool.free[smallest].Pix)
		layerPool.free[smallest] = layerPool.free[len(layerPool.free)-1]
		layerPool.free = layerPool.free[:len(layerPool.free)-1]
	}
	if held+cap(l.Pix) <= maxFreeBytes {
		layerPool.free = append(layerPool.free, l)
	}
}

// maxBands bounds the compositor's parallelism: the preview must leave cores to decode,
// audio and the UI.
const maxBands = 6

// parallelRows splits [y0,y1) into bands and runs fn on them concurrently.
func parallelRows(y0, y1 int, fn func(y0, y1 int)) {
	rows := y1 - y0
	if rows <= 0 {
		return
	}
	n := min(runtime.GOMAXPROCS(0), maxBands, rows/16)
	if n <= 1 {
		fn(y0, y1)
		return
	}
	var wg sync.WaitGroup
	step := (rows + n - 1) / n
	for a := y0; a < y1; a += step {
		b := min(a+step, y1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(a, b)
		}()
	}
	wg.Wait()
}

// fillRect sets every pixel of r (clipped to img) to c.
func fillRect(img *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	r = r.Intersect(img.Rect)
	if r.Empty() {
		return
	}
	parallelRows(r.Min.Y, r.Max.Y, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := img.Pix[img.PixOffset(r.Min.X, y):img.PixOffset(r.Max.X, y)]
			for i := 0; i < len(row); i += 4 {
				row[i], row[i+1], row[i+2], row[i+3] = c.R, c.G, c.B, c.A
			}
		}
	})
}

// blendSolid composites colour c with extra opacity alpha over r of img.
func blendSolid(img *image.NRGBA, r image.Rectangle, c color.NRGBA, alpha float64) {
	a := uint32(float64(c.A)*clamp01(alpha) + 0.5)
	r = r.Intersect(img.Rect)
	if a == 0 || r.Empty() {
		return
	}
	parallelRows(r.Min.Y, r.Max.Y, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := img.Pix[img.PixOffset(r.Min.X, y):img.PixOffset(r.Max.X, y)]
			for i := 0; i < len(row); i += 4 {
				over(row[i:i+4:i+4], c.R, c.G, c.B, a)
			}
		}
	})
}

// overLayer composites the r region of layer l onto dst ("over"), scaled by alpha.
// Both images are non-premultiplied and share the same bounds.
func overLayer(dst, l *image.NRGBA, r image.Rectangle, alpha float64) {
	k := uint32(clamp01(alpha)*255 + 0.5)
	r = r.Intersect(dst.Rect).Intersect(l.Rect)
	if k == 0 || r.Empty() {
		return
	}
	parallelRows(r.Min.Y, r.Max.Y, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			d := dst.Pix[dst.PixOffset(r.Min.X, y):dst.PixOffset(r.Max.X, y)]
			s := l.Pix[l.PixOffset(r.Min.X, y):l.PixOffset(r.Max.X, y)]
			for i := 0; i < len(d); i += 4 {
				a := uint32(s[i+3])
				if k != 255 {
					a = (a*k + 127) / 255
				}
				over(d[i:i+4:i+4], s[i], s[i+1], s[i+2], a)
			}
		}
	})
}

// over blends the non-premultiplied colour (r,g,b) with coverage a (0..255) onto the
// non-premultiplied pixel d.
func over(d []uint8, r, g, b uint8, a uint32) {
	switch {
	case a == 0:
		return
	case a == 255:
		d[0], d[1], d[2], d[3] = r, g, b, 255
		return
	}
	da := uint32(d[3])
	ia := 255 - a
	if da == 255 {
		d[0] = uint8((uint32(r)*a + uint32(d[0])*ia + 127) / 255)
		d[1] = uint8((uint32(g)*a + uint32(d[1])*ia + 127) / 255)
		d[2] = uint8((uint32(b)*a + uint32(d[2])*ia + 127) / 255)
		return
	}
	// General case, everything scaled by 255: out alpha = a + da*(1-a).
	sa := a * 255
	dw := da * ia
	oa := sa + dw
	d[0] = uint8((uint32(r)*sa + uint32(d[0])*dw + oa/2) / oa)
	d[1] = uint8((uint32(g)*sa + uint32(d[1])*dw + oa/2) / oa)
	d[2] = uint8((uint32(b)*sa + uint32(d[2])*dw + oa/2) / oa)
	d[3] = uint8((oa + 127) / 255)
}

func clamp01(v float64) float64 {
	switch {
	case v < 0 || v != v:
		return 0
	case v > 1:
		return 1
	}
	return v
}

// isNil reports whether v is nil or an interface holding a nil pointer.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
		return rv.IsNil()
	}
	return false
}
