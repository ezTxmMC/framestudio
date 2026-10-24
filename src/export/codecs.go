package export

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func codecName(f Format) string {
	switch f {
	case FormatHEVC:
		return "hevc"
	case FormatAV1:
		return "av1"
	}
	return "h264"
}

func encoderInput(o Options) []string {
	if strings.HasSuffix(o.encoder, "_vaapi") {
		return []string{"-vaapi_device", o.device}
	}
	return nil
}

func codecArgs(o Options) []string {
	name := o.encoder
	if name == "" {
		switch o.Format {
		case FormatHEVC:
			name = "libx265"
		case FormatAV1:
			name = "libaom-av1"
		default:
			name = "libx264"
		}
	}
	quality := "23"
	if o.Quality == QualityHigh {
		quality = "18"
	}
	if o.Quality == QualityLow {
		quality = "28"
	}
	filter := o.fpsFilter
	args := []string{}
	addFilter := func(s string) {
		if filter != "" {
			filter += ","
		}
		filter += s
	}
	switch {
	case strings.HasSuffix(name, "_vaapi"):
		addFilter("format=nv12,hwupload")
		args = append(args, "-c:v", name, "-qp", quality)
	case strings.HasSuffix(name, "_nvenc"):
		args = append(args, "-c:v", name, "-preset", "p4", "-rc", "vbr", "-cq", quality, "-b:v", "0", "-pix_fmt", "yuv420p")
	case strings.HasSuffix(name, "_qsv"):
		args = append(args, "-c:v", name, "-global_quality", quality, "-pix_fmt", "nv12")
	case name == "libaom-av1":
		args = append(args, "-c:v", name, "-crf", quality, "-b:v", "0", "-cpu-used", "6", "-threads", "2", "-pix_fmt", "yuv420p")
	case name == "libsvtav1":
		args = append(args, "-c:v", name, "-crf", quality, "-preset", "8", "-svtav1-params", "lp=2", "-pix_fmt", "yuv420p")
	default:
		args = append(args, "-c:v", name, "-preset", "medium", "-crf", quality, "-pix_fmt", "yuv420p")
		if name == "libx265" {
			args = append(args, "-x265-params", "log-level=error:pools=2")
		}
	}
	if filter != "" {
		args = append([]string{"-vf", filter}, args...)
	}
	if o.Format == FormatHEVC {
		args = append(args, "-tag:v", "hvc1")
	}
	return append(args, "-movflags", "+faststart")
}

// resolveEncoder verifies a real encode before consuming rendered project frames.
// Merely being listed by ffmpeg does not mean a GPU or its driver can use a codec.
func resolveEncoder(ctx context.Context, o Options) (Options, error) {
	ff, err := execLookPath("ffmpeg")
	if err != nil {
		return o, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(checkCtx, ff, "-hide_banner", "-encoders").Output()
	if err != nil {
		return o, fmt.Errorf("FFmpeg-Encoder konnten nicht gelesen werden: %w", err)
	}
	available := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 {
			available[fields[1]] = true
		}
	}
	var candidates []Options
	mode := o.Encoder
	if mode == "" {
		mode = "software"
	}
	if mode != "software" {
		for _, backend := range []string{"vaapi", "nvenc", "qsv"} {
			if mode != "auto" && mode != backend {
				continue
			}
			candidate := o
			candidate.encoder = codecName(o.Format) + "_" + backend
			if backend == "vaapi" {
				devices, _ := filepath.Glob("/dev/dri/renderD*")
				for _, device := range devices {
					candidate.device = device
					candidates = append(candidates, candidate)
				}
			} else {
				candidates = append(candidates, candidate)
			}
		}
	}
	if mode == "software" || mode == "auto" {
		names := []string{"libx264"}
		if o.Format == FormatHEVC {
			names = []string{"libx265"}
		}
		if o.Format == FormatAV1 {
			names = []string{"libsvtav1", "libaom-av1"}
		}
		for _, name := range names {
			candidate := o
			candidate.encoder = name
			candidates = append(candidates, candidate)
		}
	}
	for _, candidate := range candidates {
		if !available[candidate.encoder] {
			continue
		}
		trial, stop := context.WithTimeout(checkCtx, 5*time.Second)
		// Test the requested dimensions, since encoder limits differ by hardware.
		args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error"}, encoderInput(candidate)...)
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("color=s=%dx%d:r=25:d=0.04", o.Width, o.Height))
		args = append(args, codecArgs(candidate)...)
		args = append(args, "-frames:v", "1", "-f", "null", "-")
		err := exec.CommandContext(trial, ff, args...).Run()
		stop()
		if err == nil {
			return candidate, nil
		}
		if ctx.Err() != nil {
			return o, ctx.Err()
		}
	}
	return o, fmt.Errorf("Kein funktionsfähiger %s-Encoder für %s. FFmpeg-Codec-Unterstützung und Grafiktreiber prüfen oder Automatisch / Software wählen.", mode, Label(o.Format))
}
