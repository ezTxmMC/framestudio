package shell

import (
	"os"
	"sync"
)

// saveLog serialises all writes of project files. Every save takes a sequence number
// when its snapshot is taken (on the UI thread); a write whose snapshot is older than
// what is already on disk is skipped, so a slow autosave can never replace a newer
// explicit save. Safe for concurrent use.
type saveLog struct {
	mu    sync.Mutex
	seq   uint64
	paths map[string]*pathLog
}

// pathLog is the write state of one file.
type pathLog struct {
	mu   sync.Mutex
	disk uint64 // sequence number of the snapshot on disk
}

func newSaveLog() *saveLog { return &saveLog{paths: map[string]*pathLog{}} }

// next returns a new sequence number, newer than every earlier one.
func (l *saveLog) next() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	return l.seq
}

func (l *saveLog) path(p string) *pathLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	pl := l.paths[p]
	if pl == nil {
		pl = &pathLog{}
		l.paths[p] = pl
	}
	return pl
}

// write runs fn for the snapshot taken at seq while holding the file's lock, unless a
// newer snapshot is already on disk. It reports whether fn ran.
func (l *saveLog) write(path string, seq uint64, fn func() error) (bool, error) {
	pl := l.path(path)
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.disk > seq {
		return false, nil
	}
	if err := fn(); err != nil {
		return true, err
	}
	pl.disk = seq
	return true, nil
}

// restore puts back the file content an autosave replaced and blocks every autosave
// whose snapshot is older than now. Used when the user discards changes that an
// autosave wrote meanwhile. prev nil means the file did not exist.
func (l *saveLog) restore(path string, prev []byte, existed bool) error {
	seq := l.next()
	pl := l.path(path)
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.disk = seq
	if !existed {
		return os.Remove(path)
	}
	tmp := path + ".restore"
	if err := os.WriteFile(tmp, prev, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// block makes every pending autosave of path with an older snapshot skip its write,
// waiting for one that is writing right now.
func (l *saveLog) block(path string) {
	seq := l.next()
	pl := l.path(path)
	pl.mu.Lock()
	if pl.disk < seq {
		pl.disk = seq
	}
	pl.mu.Unlock()
}
