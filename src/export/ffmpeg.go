package export

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"framestudio-proto/src/workgate"
)

var execLookPath = exec.LookPath

// tail keeps the last bytes written to it, for ffmpeg error messages.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

const tailSize = 4096

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > tailSize {
		t.buf = t.buf[len(t.buf)-tailSize:]
	}
	return len(b), nil
}

// Lines returns the last n non-empty lines.
func (t *tail) Lines(n int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	for _, l := range strings.Split(string(t.buf), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ffmpegError turns a failed ffmpeg run into a German message with the stderr tail.
func ffmpegError(err error, t *tail) error {
	msg := t.Lines(3)
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("ffmpeg ist fehlgeschlagen: %s", msg)
}

// newCommand prepares a quiet, overwriting ffmpeg run whose stderr goes to the returned tail.
// The process is killed when ctx is cancelled.
func newCommand(ctx context.Context, args ...string) (*exec.Cmd, *tail) {
	base := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y"}
	// exports are background work: low CPU/IO priority, frozen while the user plays
	cmd := workgate.Default().Command(ctx, workgate.PrioBackground, "ffmpeg", append(base, args...)...)
	t := &tail{}
	cmd.Stderr = t
	cmd.WaitDelay = 2 * time.Second
	return cmd, t
}

// tempOutput returns a hidden temporary path next to final; ffmpeg writes there and the
// file is renamed on success, so a failed run never clobbers an existing export.
func tempOutput(final string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(final), "."+filepath.Base(final)+".*.part")
	if err != nil {
		return "", fmt.Errorf("Ausgabedatei kann nicht angelegt werden: %w", err)
	}
	f.Close()
	return f.Name(), nil
}

// reporter throttles progress callbacks to ~10 per second and computes the ETA.
type reporter struct {
	fn    func(Progress)
	start time.Time
	last  time.Time
	stage string
	note  string
}

func newReporter(fn func(Progress)) *reporter {
	return &reporter{fn: fn, start: time.Now()}
}

func (r *reporter) Stage(s string, frame, total int) {
	r.stage = s
	r.emit(frame, total, true)
}

func (r *reporter) Frame(frame, total int) { r.emit(frame, total, frame >= total) }

func (r *reporter) emit(frame, total int, force bool) {
	if r.fn == nil {
		return
	}
	now := time.Now()
	if !force && now.Sub(r.last) < 100*time.Millisecond {
		return
	}
	r.last = now
	pr := Progress{Frame: frame, Total: total, Elapsed: now.Sub(r.start), Stage: r.stage, Note: r.note}
	if frame > 0 && total > frame {
		pr.ETA = time.Duration(float64(pr.Elapsed) / float64(frame) * float64(total-frame))
	}
	r.fn(pr)
}

// errCanceled marks pipeline shutdown caused by a failing consumer.
var errCanceled = errors.New("abgebrochen")
