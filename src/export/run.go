package export

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
)

const gifMaxFPS = 15

// workers is the size of the render pool.
func workers() int { return min(max(runtime.NumCPU()-1, 1), 6) }

// pipeline runs work(i) for i in [0,n) on a pool of workers and hands the results to
// sink strictly in order. It stops at the first error or when ctx is cancelled, and
// returns only after all workers have finished.
func pipeline[T any](ctx context.Context, n, pool int, work func(i int) (T, error), sink func(i int, v T) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		v   T
		err error
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	order := make(chan chan result, pool)
	sem := make(chan struct{}, pool)
	go func() {
		defer close(order)
		for i := range n {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			ch := make(chan result, 1)
			select {
			case order <- ch:
			case <-ctx.Done():
				<-sem
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				v, err := work(i)
				<-sem
				ch <- result{v, err}
			}()
		}
	}()
	i := 0
	for ch := range order {
		res := <-ch
		if res.err == nil {
			res.err = sink(i, res.v)
		}
		if res.err != nil {
			cancel()
			for range order {
			}
			return res.err
		}
		i++
	}
	if i < n {
		if err := ctx.Err(); err != nil {
			return err
		}
		return errCanceled
	}
	return nil
}

// timelineFrames lists the timeline frames sampled at outFPS over [from,to).
func timelineFrames(from, to, fps, outFPS int) []int {
	if outFPS >= fps {
		out := make([]int, 0, to-from)
		for f := from; f < to; f++ {
			out = append(out, f)
		}
		return out
	}
	n := ((to-from)*outFPS + fps - 1) / fps
	out := make([]int, n)
	for k := range out {
		out[k] = from + k*fps/outFPS
	}
	return out
}

// frameRenderer renders timeline frames at the export size with a sequential decoder.
type frameRenderer struct {
	p    *model.Project
	r    *render.Renderer
	w, h int
	src  *seqSource
}

func newFrameRenderer(p *model.Project, r *render.Renderer, w, h, pool int) *frameRenderer {
	var fallback render.VideoSource
	switch {
	case r.Media != nil:
		fallback = r.Media
	case r.Video != nil:
		fallback = r.Video
	}
	return &frameRenderer{p: p, r: r, w: w, h: h, src: newSeqSource(fallback, pool+2)}
}

// frame returns frame f as a tightly packed w x h NRGBA image.
func (fr *frameRenderer) frame(f int) *image.NRGBA {
	img := fr.r.FrameWith(fr.p, f, fr.w, fr.h, render.Options{Video: fr.src})
	want := image.Rect(0, 0, fr.w, fr.h)
	if img != nil && img.Rect == want && img.Stride == 4*fr.w {
		return img
	}
	out := image.NewNRGBA(want)
	if img != nil {
		draw.Draw(out, want, img, img.Rect.Min, draw.Src)
	}
	return out
}

func (fr *frameRenderer) Close() { fr.src.Close() }

func fileExists(m model.MediaItem) bool {
	st, err := os.Stat(m.Path)
	return err == nil && !st.IsDir()
}

// videoArgs returns the ffmpeg encoder arguments of a video format.
func videoArgs(o Options) []string {
	pick := func(hi, mid, lo string) string {
		switch o.Quality {
		case QualityHigh:
			return hi
		case QualityLow:
			return lo
		}
		return mid
	}
	switch o.Format {
	case FormatMP4:
		return []string{"-c:v", "libx264", "-preset", "medium", "-crf", pick("18", "23", "28"),
			"-pix_fmt", "yuv420p", "-movflags", "+faststart"}
	case FormatWebM:
		return []string{"-c:v", "libvpx-vp9", "-crf", pick("30", "34", "40"), "-b:v", "0",
			"-row-mt", "1", "-deadline", "good", "-cpu-used", "2", "-pix_fmt", "yuv420p"}
	case FormatGIF:
		colors := pick("256", "128", "64")
		return []string{"-vf", fmt.Sprintf("scale=%d:-1:flags=lanczos,split[a][b];"+
			"[a]palettegen=max_colors=%s:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4", o.Width, colors),
			"-loop", "0"}
	}
	return nil
}

func audioArgs(f Format) []string {
	switch f {
	case FormatMP4:
		return []string{"-c:a", "aac", "-b:a", "192k"}
	case FormatWebM:
		return []string{"-c:a", "libopus", "-b:a", "160k"}
	}
	return []string{"-c:a", "pcm_s16le"}
}

func runVideo(ctx context.Context, p *model.Project, r *render.Renderer, o Options, rep *reporter) error {
	from, to := Range(p, o)
	fps := p.Settings.FPS
	outFPS := fps
	if o.Format == FormatGIF {
		outFPS = min(fps, gifMaxFPS)
	}
	frames := timelineFrames(from, to, fps, outFPS)

	tmp, err := tempOutput(o.Path)
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // no-op after the final rename

	args := []string{"-f", "rawvideo", "-pix_fmt", "rgba", "-s", fmt.Sprintf("%dx%d", o.Width, o.Height),
		"-r", strconv.Itoa(outFPS), "-i", "-"}
	var mix AudioMix
	hasAudio := false
	if o.IncludeAudio && o.Format != FormatGIF {
		mix, hasAudio = BuildAudioMix(p, from, to, 1, fileExists)
	}
	if hasAudio {
		for _, in := range mix.Inputs {
			args = append(args, "-i", in)
		}
		args = append(args, "-filter_complex", mix.Filter, "-map", "0:v", "-map", "[aout]")
		args = append(args, audioArgs(o.Format)...)
	}
	args = append(args, videoArgs(o)...)
	args = append(args, "-f", string(o.Format), tmp)

	cmd, stderr := newCommand(ctx, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg kann nicht gestartet werden: %w", err)
	}

	fr := newFrameRenderer(p, r, o.Width, o.Height, workers())
	defer fr.Close()
	rep.Stage("Bilder rendern", 0, len(frames))
	perr := pipeline(ctx, len(frames), workers(),
		func(i int) (*image.NRGBA, error) { return fr.frame(frames[i]), nil },
		func(i int, img *image.NRGBA) error {
			if _, err := stdin.Write(img.Pix[:4*o.Width*o.Height]); err != nil {
				return err
			}
			rep.Frame(i+1, len(frames))
			return nil
		})
	stdin.Close()
	if perr == nil {
		rep.Stage("Abschließen", len(frames), len(frames))
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if werr != nil {
		return ffmpegError(werr, stderr)
	}
	if perr != nil {
		return perr
	}
	return finish(tmp, o.Path)
}

// finish moves a completed temporary output into place.
func finish(tmp, final string) error {
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("Ausgabe kann nicht nach %s geschrieben werden: %w", final, err)
	}
	return nil
}

func runWAV(ctx context.Context, p *model.Project, o Options, rep *reporter) error {
	from, to := Range(p, o)
	mix, ok := BuildAudioMix(p, from, to, 0, fileExists)
	if !ok {
		return errors.New("Die Audiodateien im Bereich fehlen. Verknüpfe die Medien neu und versuche es erneut.")
	}
	tmp, err := tempOutput(o.Path)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	var args []string
	for _, in := range mix.Inputs {
		args = append(args, "-i", in)
	}
	args = append(args, "-filter_complex", mix.Filter, "-map", "[aout]")
	args = append(args, audioArgs(FormatWAV)...)
	args = append(args, "-progress", "pipe:1", "-nostats", "-f", "wav", tmp)
	cmd, stderr := newCommand(ctx, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	total := to - from
	rep.Stage("Audio mischen", 0, total)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg kann nicht gestartet werden: %w", err)
	}
	readProgress(stdout, p.Settings.FPS, total, rep)
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ffmpegError(err, stderr)
	}
	rep.Frame(total, total)
	return finish(tmp, o.Path)
}

// readProgress turns ffmpeg "-progress" output (out_time_us=...) into frame progress.
func readProgress(rd io.Reader, fps, total int, rep *reporter) {
	sc := bufio.NewScanner(rd)
	for sc.Scan() {
		v, ok := strings.CutPrefix(sc.Text(), "out_time_us=")
		if !ok {
			continue
		}
		us, err := strconv.ParseInt(v, 10, 64)
		if err != nil || us < 0 {
			continue
		}
		rep.Frame(min(int(us*int64(fps)/1e6), total-1), total)
	}
}

// runPNG renders the sequence into a hidden sibling folder and moves it into place only
// when every frame is written, so a failed or cancelled run never touches an earlier
// export. Old frames of the same name that are not part of the new range are removed.
func runPNG(ctx context.Context, p *model.Project, r *render.Renderer, o Options, rep *reporter) error {
	from, to := Range(p, o)
	dir := filepath.Clean(o.Path)
	name := filepath.Base(dir)
	tmp, err := os.MkdirTemp(filepath.Dir(dir), "."+name+".*.part")
	if err != nil {
		return fmt.Errorf("Ordner neben %s kann nicht angelegt werden: %w", dir, err)
	}
	defer os.RemoveAll(tmp) // empty after a successful swap
	pool := workers()
	fr := newFrameRenderer(p, r, o.Width, o.Height, pool)
	defer fr.Close()
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	total := to - from
	rep.Stage("Bilder rendern", 0, total)
	err = pipeline(ctx, total, pool,
		func(i int) (struct{}, error) {
			if ctx.Err() != nil {
				return struct{}{}, ctx.Err()
			}
			path := filepath.Join(tmp, frameName(name, i))
			f, err := os.Create(path)
			if err != nil {
				return struct{}{}, fmt.Errorf("Bild %s kann nicht geschrieben werden: %w", path, err)
			}
			err = enc.Encode(f, fr.frame(from+i))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return struct{}{}, fmt.Errorf("Bild %s kann nicht geschrieben werden: %w", path, err)
			}
			return struct{}{}, nil
		},
		func(i int, _ struct{}) error {
			rep.Frame(i+1, total)
			return nil
		})
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return swapSequence(tmp, dir, name, total)
}

func frameName(name string, i int) string { return fmt.Sprintf("%s_%06d.png", name, i) }

// swapSequence moves a finished sequence from tmp into dir. If dir does not exist the
// folder is renamed as a whole; otherwise stale frames of an earlier export are removed
// and the new ones moved in. Other files in dir are left alone.
func swapSequence(tmp, dir, name string, total int) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(tmp, dir); err != nil {
			return fmt.Errorf("Ordner %s kann nicht angelegt werden: %w", dir, err)
		}
		return nil
	}
	old, _ := filepath.Glob(filepath.Join(dir, name+"_[0-9]*.png"))
	for _, path := range old {
		num := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), name+"_"), ".png")
		if i, err := strconv.Atoi(num); err == nil && i >= total {
			os.Remove(path)
		}
	}
	for i := range total {
		if err := os.Rename(filepath.Join(tmp, frameName(name, i)), filepath.Join(dir, frameName(name, i))); err != nil {
			return fmt.Errorf("Bilder können nicht nach %s verschoben werden: %w", dir, err)
		}
	}
	return nil
}
