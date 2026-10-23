package audio

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// ErrNoPlayer is reported when no audio player program is installed.
var ErrNoPlayer = errors.New("Wiedergabe ohne Ton: Kein Audioplayer gefunden. PipeWire (pw-play), PulseAudio (paplay) oder ffmpeg (ffplay) installieren.")

// Output receives the engine's s16le stereo 48 kHz stream. Write blocks while the
// device is busy, which paces the engine.
type Output interface {
	io.Writer
	// Latency is the time from a Write returning to its first sample being heard.
	Latency() time.Duration
	// Close stops the output and waits for it to exit.
	Close() error
}

// player is an external program that plays raw PCM from stdin.
type player struct {
	name    string
	args    []string
	latency time.Duration // buffering inside the player, beyond the pipe
}

// players in order of preference: low latency first.
var players = []player{
	{"pw-play", []string{"--raw", "--format", "s16", "--rate", strconv.Itoa(SampleRate),
		"--channels", strconv.Itoa(Channels), "--latency", "30ms", "-"}, 42 * time.Millisecond}, // ~2 quanta of 1024
	{"paplay", []string{"--raw", "--format=s16le", "--rate=" + strconv.Itoa(SampleRate),
		"--channels=" + strconv.Itoa(Channels), "--latency-msec=30"}, 50 * time.Millisecond},
	{"ffplay", []string{"-nodisp", "-loglevel", "quiet", "-fflags", "nobuffer", "-probesize", "32",
		"-analyzeduration", "0", "-f", "s16le", "-ar", strconv.Itoa(SampleRate), "-ch_layout", "stereo", "-i", "-"},
		150 * time.Millisecond},
}

// pipeSize is the stdin pipe capacity we ask for: one page (~21 ms of audio), so the
// pipe adds little latency and blocking writes pace the engine.
const pipeSize = 4096

// procOutput streams into a player process.
type procOutput struct {
	cmd     *exec.Cmd
	w       *os.File
	latency time.Duration
	exited  chan struct{}
}

// OpenPlayer starts the first available player. It returns ErrNoPlayer when none is
// installed.
func OpenPlayer() (Output, error) {
	var startErr error
	for _, p := range players {
		bin, err := lookPath(p.name)
		if err != nil {
			continue
		}
		out, err := startPlayer(bin, p)
		if err == nil {
			return out, nil
		}
		if startErr == nil {
			startErr = fmt.Errorf("Wiedergabe ohne Ton: %s lässt sich nicht starten (%v). Audioausgabe des Systems prüfen.", p.name, err)
		}
	}
	if startErr != nil {
		return nil, startErr
	}
	return nil, ErrNoPlayer
}

func startPlayer(bin string, p player) (*procOutput, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	size := setPipeSize(w, pipeSize)
	cmd := exec.Command(bin, p.args...)
	cmd.Stdin = r
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	r.Close() // the player holds its own end
	o := &procOutput{cmd: cmd, w: w, exited: make(chan struct{}),
		latency: p.latency + time.Duration(size)*time.Second/(SampleRate*bytesPerFrame)}
	go func() {
		_ = cmd.Wait()
		close(o.exited)
	}()
	return o, nil
}

// setPipeSize shrinks the pipe buffer (Linux) and returns the resulting capacity.
func setPipeSize(f *os.File, size int) int {
	const fSetPipeSz, fGetPipeSz = 1031, 1032
	got := 65536
	rc, err := f.SyscallConn()
	if err != nil {
		return got
	}
	_ = rc.Control(func(fd uintptr) {
		if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, fd, fSetPipeSz, uintptr(size)); e != 0 {
			return
		}
		if n, _, e := syscall.Syscall(syscall.SYS_FCNTL, fd, fGetPipeSz, 0); e == 0 {
			got = int(n)
		}
	})
	return got
}

func (o *procOutput) Write(b []byte) (int, error) {
	select {
	case <-o.exited:
		return 0, errors.New("Audioplayer wurde beendet")
	default:
	}
	return o.w.Write(b)
}

func (o *procOutput) Latency() time.Duration { return o.latency }

// Close ends the stream; the player drains its short buffer and exits, or is killed.
func (o *procOutput) Close() error {
	err := o.w.Close()
	select {
	case <-o.exited:
	case <-time.After(300 * time.Millisecond):
		_ = o.cmd.Process.Kill()
		<-o.exited
	}
	return err
}

// nullOutput discards audio in real time when no player is available, so meters keep
// working.
type nullOutput struct {
	next time.Time
	stop chan struct{}
}

func newNullOutput() *nullOutput { return &nullOutput{stop: make(chan struct{})} }

func (o *nullOutput) Write(b []byte) (int, error) {
	d := time.Duration(len(b)/bytesPerFrame) * time.Second / SampleRate
	now := time.Now()
	if o.next.Before(now.Add(-100 * time.Millisecond)) {
		o.next = now
	}
	o.next = o.next.Add(d)
	t := time.NewTimer(time.Until(o.next))
	defer t.Stop()
	select {
	case <-t.C:
		return len(b), nil
	case <-o.stop:
		return 0, io.ErrClosedPipe
	}
}

func (o *nullOutput) Latency() time.Duration { return 0 }

func (o *nullOutput) Close() error {
	select {
	case <-o.stop:
	default:
		close(o.stop)
	}
	return nil
}
