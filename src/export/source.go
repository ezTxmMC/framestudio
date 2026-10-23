package export

import (
	"errors"
	"image"
	"sync"
	"sync/atomic"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
)

// Tuning of the sequential decoder.
const (
	maxReaders = 3  // open decoders per media item and size (e.g. the same file on two tracks)
	maxSkip    = 50 // frames a decoder reads ahead instead of reopening
	maxOpens   = 24 // decoder starts per media item before giving up on sequential access
)

// seqSource is a render.VideoSource for export: it keeps one media.FrameReader per clip
// playing through a media item and advances it frame by frame. A short ring of recent
// frames absorbs the slight reordering of the parallel renderers; anything else falls back
// to the random-access source.
type seqSource struct {
	open     func(m model.MediaItem, n int, fps model.Rate, w, h int) (media.FrameReader, error)
	fallback render.VideoSource
	keep     int // frames kept per reader

	mu      sync.Mutex
	closed  bool
	tick    int
	readers map[seqKey][]*seqReader
	opens   map[seqKey]int
}

type seqKey struct {
	id, path string
	fps      model.Rate
	w, h     int
}

type seqReader struct {
	mu   sync.Mutex // serialises decoding
	r    media.FrameReader
	ring []seqFrame
	used int // guarded by seqSource.mu

	// Readable without mu so lookups never wait for a decode.
	next   atomic.Int64 // source frame the next Next() returns
	oldest atomic.Int64 // oldest buffered frame
	dead   atomic.Bool
}

type seqFrame struct {
	n   int
	img image.Image
}

func newSeqSource(fallback render.VideoSource, keep int) *seqSource {
	return &seqSource{
		open: media.OpenVideo, fallback: fallback, keep: max(keep, 2),
		readers: map[seqKey][]*seqReader{}, opens: map[seqKey]int{},
	}
}

var errNoSource = errors.New("keine Videoquelle")

func (s *seqSource) VideoFrame(m model.MediaItem, n int, fps model.Rate, w, h int) (image.Image, error) {
	if rd := s.reader(m, n, fps, w, h); rd != nil {
		if img, ok := rd.frame(n, s.keep); ok {
			return img, nil
		}
	}
	if s.fallback == nil {
		return nil, errNoSource
	}
	return s.fallback.VideoFrame(m, n, fps, w, h)
}

// reader finds a decoder that can serve frame n, opening one if needed.
func (s *seqSource) reader(m model.MediaItem, n int, fps model.Rate, w, h int) *seqReader {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.open == nil {
		return nil
	}
	key := seqKey{m.ID, m.Path, fps, w, h}
	s.tick++
	list := s.readers[key]
	var best *seqReader
	for _, rd := range list {
		if rd.serves(n) && (best == nil || rd.next.Load() < best.next.Load()) {
			best = rd
		}
	}
	if best != nil {
		best.used = s.tick
		return best
	}
	if s.opens[key] >= maxOpens {
		return nil
	}
	s.opens[key]++
	fr, err := s.open(m, n, fps, w, h)
	if err != nil {
		s.opens[key] = maxOpens // decoder unusable for this media: random access only
		return nil
	}
	rd := &seqReader{r: fr, used: s.tick}
	rd.next.Store(int64(n))
	rd.oldest.Store(int64(n))
	if len(list) >= maxReaders {
		lru := 0
		for i, o := range list {
			if o.used < list[lru].used {
				lru = i
			}
		}
		list[lru].close()
		list = append(list[:lru], list[lru+1:]...)
	}
	s.readers[key] = append(list, rd)
	return rd
}

// Close stops all decoders. Later requests use the fallback.
func (s *seqSource) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, list := range s.readers {
		for _, rd := range list {
			rd.close()
		}
	}
	s.readers = nil
}

// serves reports, without waiting for a decode, whether frame n is buffered or a short
// read ahead. It is a hint: frame re-checks under the lock.
func (rd *seqReader) serves(n int) bool {
	v := int64(n)
	return !rd.dead.Load() && v >= rd.oldest.Load() && v < rd.next.Load()+maxSkip
}

// frame returns source frame n, decoding forward as needed.
func (rd *seqReader) frame(n, keep int) (image.Image, bool) {
	rd.mu.Lock()
	defer rd.mu.Unlock()
	for _, f := range rd.ring {
		if f.n == n {
			return f.img, true
		}
	}
	next := int(rd.next.Load())
	if rd.dead.Load() || n < next {
		return nil, false
	}
	for ; next <= n; next++ {
		img, err := rd.r.Next()
		if err != nil || img == nil {
			rd.dead.Store(true)
			rd.r.Close()
			return nil, false
		}
		rd.ring = append(rd.ring, seqFrame{next, img})
		if len(rd.ring) > keep {
			rd.ring = rd.ring[len(rd.ring)-keep:]
		}
		rd.oldest.Store(int64(rd.ring[0].n))
		rd.next.Store(int64(next + 1))
	}
	return rd.ring[len(rd.ring)-1].img, true
}

func (rd *seqReader) close() {
	rd.mu.Lock()
	defer rd.mu.Unlock()
	if !rd.dead.Swap(true) {
		rd.r.Close()
	}
	rd.ring = nil
}
