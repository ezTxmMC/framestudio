package display

import (
	"bytes"
	"runtime"
	"strconv"
)

// goid returns the current goroutine's id. It is only compared with mainGID to decide
// whether a GLFW call can run inline (we are on Fyne's main loop) or must be queued.
func goid() uint64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i > 0 {
		b = b[:i]
	}
	id, _ := strconv.ParseUint(string(b), 10, 64)
	return id
}
