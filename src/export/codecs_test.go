package export

import (
	"context"
	"encoding/json"
	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodecExportsAndReimport(t *testing.T) {
	needFFmpeg(t)
	available, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []Format{FormatMP4, FormatHEVC, FormatAV1} {
		t.Run(string(format), func(t *testing.T) {
			encoder := map[Format]string{FormatMP4: "libx264", FormatHEVC: "libx265", FormatAV1: "libaom-av1"}[format]
			if !strings.Contains(string(available), encoder) {
				t.Skip("software encoder missing")
			}
			p := model.NewProject("codec")
			p.Settings.Width, p.Settings.Height = 64, 64
			p.Settings.FPS = model.NTSC30
			_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "color", 0, 5))
			cache := media.NewCache()
			defer cache.Close()
			path := filepath.Join(t.TempDir(), "encoded"+Extension(format))
			o := Options{Format: format, Path: path, Width: 64, Height: 64, Encoder: "software", Quality: QualityMedium}
			if err := Run(context.Background(), p, render.New(cache), o, nil); err != nil {
				t.Fatal(err)
			}
			raw, err := exec.Command("ffprobe", "-v", "error", "-show_streams", "-of", "json", path).Output()
			if err != nil {
				t.Fatal(err)
			}
			var probe struct {
				Streams []struct {
					CodecName string `json:"codec_name"`
					Rate      string `json:"avg_frame_rate"`
					Frames    string `json:"nb_frames"`
				}
			}
			if err := json.Unmarshal(raw, &probe); err != nil {
				t.Fatal(err)
			}
			if len(probe.Streams) != 1 || probe.Streams[0].CodecName != codecName(format) || probe.Streams[0].Rate != "30000/1001" || probe.Streams[0].Frames != "5" {
				t.Fatalf("codec/rate/frame mismatch: %s", raw)
			}
			source, err := media.Probe(path)
			if err != nil {
				t.Fatal(err)
			}
			if !source.FPS.Equal(model.NTSC30) || source.Width != 64 {
				t.Fatal("source probe")
			}
			frame, err := cache.VideoFrame(source, 2, model.NTSC30, 64, 64)
			if err != nil || frame == nil {
				t.Fatalf("reimport decode: %v", err)
			}
		})
	}
}
func TestEncoderFallbackAndHardwareFilter(t *testing.T) {
	needFFmpeg(t)
	o, err := resolveEncoder(context.Background(), Options{Format: FormatMP4, Width: 64, Height: 64, Encoder: "auto"})
	if err != nil || o.encoder == "" {
		t.Fatalf("auto encoder: %+v %v", o, err)
	}
	args := strings.Join(codecArgs(Options{Format: FormatHEVC, encoder: "hevc_vaapi", device: "/dev/dri/renderD128", fpsFilter: "fps=25"}), " ")
	if !strings.Contains(args, "fps=25,format=nv12,hwupload") || !strings.Contains(args, "-tag:v hvc1") {
		t.Fatal(args)
	}
	if _, err := os.Stat("/dev/dri"); os.IsNotExist(err) {
		if _, err := resolveEncoder(context.Background(), Options{Format: FormatMP4, Width: 64, Height: 64, Encoder: "vaapi"}); err == nil {
			t.Fatal("explicit VAAPI silently fell back")
		}
	}
}
