package media

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register decoders
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"framestudio-proto/src/model"
)

// ErrNoFFmpeg is returned for video and audio work when ffmpeg is not installed.
var ErrNoFFmpeg = errors.New("ffmpeg ist nicht installiert. ffmpeg installieren und Frame Studio neu starten, um Video und Audio zu verwenden")

var extKinds = map[string]model.ClipKind{
	".png": model.KindImage, ".jpg": model.KindImage, ".jpeg": model.KindImage, ".gif": model.KindImage,
	".bmp": model.KindImage, ".webp": model.KindImage, ".tif": model.KindImage, ".tiff": model.KindImage,
	".mp4": model.KindVideo, ".mov": model.KindVideo, ".mkv": model.KindVideo, ".webm": model.KindVideo,
	".avi": model.KindVideo, ".m4v": model.KindVideo,
	".wav": model.KindAudio, ".mp3": model.KindAudio, ".flac": model.KindAudio, ".ogg": model.KindAudio,
	".m4a": model.KindAudio, ".aac": model.KindAudio, ".opus": model.KindAudio,
}

var extOrder = []string{
	".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".tif", ".tiff",
	".mp4", ".mov", ".mkv", ".webm", ".avi", ".m4v",
	".wav", ".mp3", ".flac", ".ogg", ".m4a", ".aac", ".opus",
}

var tools struct {
	once            sync.Once
	ffmpeg, ffprobe string
}

func lookTools() {
	tools.once.Do(func() {
		tools.ffmpeg, _ = exec.LookPath("ffmpeg")
		tools.ffprobe, _ = exec.LookPath("ffprobe")
	})
}

func ffmpegPath() (string, error) {
	lookTools()
	if tools.ffmpeg == "" {
		return "", ErrNoFFmpeg
	}
	return tools.ffmpeg, nil
}

// probeFile inspects an image without ffmpeg or a video/audio file with ffprobe.
func probeFile(path string) (model.MediaItem, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	base := filepath.Base(abs)
	kind, ok := KindForPath(abs)
	if !ok {
		return model.MediaItem{}, fmt.Errorf("„%s“: Dateityp wird nicht unterstützt", base)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return model.MediaItem{}, fmt.Errorf("„%s“: Datei nicht gefunden", base)
	}
	if st.IsDir() {
		return model.MediaItem{}, fmt.Errorf("„%s“ ist ein Ordner", base)
	}
	m := model.MediaItem{
		ID:   model.NewID(),
		Name: strings.TrimSuffix(base, filepath.Ext(base)),
		Path: abs,
		Kind: kind,
	}
	if kind == model.KindImage {
		f, err := os.Open(abs)
		if err != nil {
			return m, fmt.Errorf("„%s“ kann nicht geöffnet werden: %v", base, err)
		}
		defer f.Close()
		cfg, _, err := image.DecodeConfig(f)
		if err != nil {
			return m, fmt.Errorf("„%s“ ist kein lesbares Bild. Die Datei ist beschädigt oder hat ein unbekanntes Format", base)
		}
		m.Width, m.Height = cfg.Width, cfg.Height
		return m, nil
	}
	lookTools()
	if tools.ffprobe == "" {
		return m, fmt.Errorf("„%s“: %w", base, ErrNoFFmpeg)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(tools.ffprobe, "-v", "error", "-print_format", "json", "-show_streams", "-show_format", abs)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return m, fmt.Errorf("„%s“ kann nicht gelesen werden. Die Datei ist beschädigt oder kein unterstütztes Format (%s)", base, firstLine(stderr.String(), err))
	}
	if err := applyProbe(&m, stdout.Bytes()); err != nil {
		return m, fmt.Errorf("„%s“: %w", base, err)
	}
	return m, nil
}

type ffprobeOut struct {
	Streams []struct {
		CodecType    string            `json:"codec_type"`
		Width        int               `json:"width"`
		Height       int               `json:"height"`
		RFrameRate   string            `json:"r_frame_rate"`
		AvgFrameRate string            `json:"avg_frame_rate"`
		Duration     string            `json:"duration"`
		Tags         map[string]string `json:"tags"`
		Disposition  struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
		SideData []struct {
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// applyProbe fills size, duration, frame rate and audio flag from ffprobe JSON.
func applyProbe(m *model.MediaItem, data []byte) error {
	var out ffprobeOut
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("ffprobe lieferte unlesbare Daten: %v", err)
	}
	hasVideo := false
	// The picture defines a video file's length: camera files often carry a few frames
	// more sound than picture, and frames past the last image cannot be decoded.
	var videoDur, audioDur float64
	for _, s := range out.Streams {
		switch s.CodecType {
		case "audio":
			m.HasAudio = true
			audioDur = max(audioDur, streamDuration(s.Duration, s.Tags))
		case "video":
			if hasVideo || s.Disposition.AttachedPic != 0 || s.Width == 0 {
				continue
			}
			hasVideo = true
			m.Width, m.Height = s.Width, s.Height
			if rotated(s.Tags["rotate"], s.SideData) {
				m.Width, m.Height = m.Height, m.Width
			}
			m.FPS = parseRate(s.RFrameRate)
			if avg := parseRate(s.AvgFrameRate); m.FPS == 0 || (m.FPS > 240 && avg > 0) {
				m.FPS = avg
			}
			videoDur = streamDuration(s.Duration, s.Tags)
		}
	}
	dur := parseFloat(out.Format.Duration)
	switch {
	case hasVideo && videoDur > 0:
		dur = videoDur
	case !hasVideo && dur <= 0:
		dur = audioDur
	}
	switch {
	case m.Kind == model.KindVideo && !hasVideo && m.HasAudio:
		m.Kind = model.KindAudio // e.g. an .mp4 that only holds sound
	case m.Kind == model.KindVideo && !hasVideo:
		return errors.New("Datei enthält weder Bild noch Ton")
	case m.Kind == model.KindAudio && !m.HasAudio:
		return errors.New("Datei enthält keine Tonspur")
	}
	if m.Kind == model.KindAudio {
		m.Width, m.Height, m.FPS = 0, 0, 0
		m.HasAudio = true
	}
	m.Duration = time.Duration(dur * float64(time.Second))
	return nil
}

// streamDuration reads a stream's duration in seconds, falling back to the DURATION
// tag ("01:02:03.040000000") that Matroska files carry instead.
func streamDuration(d string, tags map[string]string) float64 {
	if v := parseFloat(d); v > 0 {
		return v
	}
	tag := tags["DURATION"]
	if tag == "" {
		tag = tags["duration"]
	}
	parts := strings.Split(tag, ":")
	if len(parts) != 3 {
		return 0
	}
	h, mi, sec := parseFloat(parts[0]), parseFloat(parts[1]), parseFloat(parts[2])
	return h*3600 + mi*60 + sec
}

func rotated(tag string, side []struct {
	Rotation float64 `json:"rotation"`
}) bool {
	r := parseFloat(tag)
	for _, sd := range side {
		if sd.Rotation != 0 {
			r = sd.Rotation
		}
	}
	q := int(math.Abs(r)) % 360
	return q == 90 || q == 270
}

// parseRate parses ffprobe rates like "30000/1001"; invalid rates give 0.
func parseRate(s string) float64 {
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return parseFloat(s)
	}
	n, d := parseFloat(num), parseFloat(den)
	if n <= 0 || d <= 0 {
		return 0
	}
	return math.Round(n/d*1000) / 1000
}

func parseFloat(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

// firstLine condenses tool stderr into a short reason for error messages.
func firstLine(stderr string, err error) string {
	s := strings.TrimSpace(stderr)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	if s == "" && err != nil {
		s = err.Error()
	}
	return s
}
