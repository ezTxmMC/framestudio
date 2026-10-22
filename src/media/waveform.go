package media

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"os/exec"

	"framestudio-proto/src/model"
)

const (
	waveRate      = 8000 // sample rate the envelope is computed from
	peaksPerSec   = 200  // resolution of the cached envelope
	samplesPerBin = waveRate / peaksPerSec
)

// readPeaks decodes the first audio stream as 8 kHz mono and returns its peak envelope.
func readPeaks(ctx context.Context, bin string, m model.MediaItem) ([]float32, error) {
	var stderr tailBuffer
	cmd := exec.CommandContext(ctx, bin, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", m.Path, "-map", "0:a:0", "-vn", "-ac", "1", "-ar", fmt.Sprint(waveRate), "-f", "s16le", "-")
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		return nil, fmt.Errorf("Ton von „%s“ kann nicht gelesen werden: %v", m.Name, err)
	}
	peaks, readErr := envelope(stdout)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("Wellenform von „%s“ abgebrochen: %w", m.Name, ctx.Err())
	}
	if readErr != nil {
		return nil, fmt.Errorf("Ton von „%s“ kann nicht gelesen werden: %v", m.Name, readErr)
	}
	if len(peaks) == 0 {
		if waitErr != nil || stderr.String() != "" {
			return nil, fmt.Errorf("„%s“ hat keine lesbare Tonspur (%s)", m.Name, firstLine(stderr.String(), waitErr))
		}
		return nil, fmt.Errorf("„%s“ enthält keinen Ton", m.Name)
	}
	return peaks, nil
}

// envelope reads signed 16-bit little-endian samples and returns one peak per bin.
func envelope(r io.Reader) ([]float32, error) {
	var peaks []float32
	buf := make([]byte, 64<<10)
	have, peak, count := 0, 0, 0
	for {
		n, err := r.Read(buf[have:])
		have += n
		even := have &^ 1
		for i := 0; i < even; i += 2 {
			v := int(int16(binary.LittleEndian.Uint16(buf[i:])))
			if v < 0 {
				v = -v
			}
			peak = max(peak, v)
			if count++; count == samplesPerBin {
				peaks = append(peaks, min(float32(peak)/32767, 1))
				peak, count = 0, 0
			}
		}
		have = copy(buf, buf[even:have])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if count > 0 {
		peaks = append(peaks, min(float32(peak)/32767, 1))
	}
	return peaks, nil
}

// resamplePeaks reduces (max per bucket) or stretches an envelope to n buckets.
func resamplePeaks(src []float32, n int) []float32 {
	out := make([]float32, n)
	if len(src) == 0 {
		return out
	}
	for i := range out {
		lo := i * len(src) / n
		hi := (i + 1) * len(src) / n
		if hi <= lo {
			hi = lo + 1
		}
		var p float32
		for _, v := range src[lo:hi] {
			p = max(p, v)
		}
		out[i] = p
	}
	return out
}

var (
	waveBack   = color.RGBA{0x1B, 0x1C, 0x20, 0xFF}
	waveCentre = color.RGBA{0x2E, 0x3A, 0x34, 0xFF}
	waveFill   = color.RGBA{0x5B, 0xBF, 0x8A, 0xFF}
)

// drawWaveform renders a mirrored peak envelope, one column per peak.
func drawWaveform(peaks []float32, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = waveBack.R, waveBack.G, waveBack.B, 0xFF
	}
	mid := h / 2
	for x := 0; x < w; x++ {
		img.SetRGBA(x, mid, waveCentre)
	}
	amp := float32(h)/2 - 2
	for x := 0; x < w && x < len(peaks); x++ {
		d := int(peaks[x]*amp + 0.5)
		for y := mid - d; y <= mid+d; y++ {
			img.SetRGBA(x, y, waveFill)
		}
	}
	return img
}
