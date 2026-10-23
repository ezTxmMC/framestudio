package audio

import (
	"bytes"
	"encoding/binary"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
)

// Clips at odd frame offsets without fades (gains 1 and 0.5, exact in float) mix to the very
// same float samples in the engine and in export's ffmpeg mixdown: tolerance 0.
func TestEngineEqualsExportExactly(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	wav := filepath.Join(dir, "tone.wav")
	if out, err := exec.Command(ff, "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=330:duration=4:sample_rate=48000",
		"-af", "volume=0.5", "-ac", "2", "-c:a", "pcm_s16le", wav).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	cache := NewCache(nil, nil, 0)
	defer cache.Close()
	pcm, err := cache.Get(wav)
	if err != nil {
		t.Fatal(err)
	}
	<-pcm.Done()
	for _, fps := range []model.Rate{model.NTSC24, model.Rate25, model.NTSC30, model.NTSC60, {Num: 2997, Den: 100}} {
		t.Run(fps.String(), func(t *testing.T) {
			p := model.NewProject("t")
			p.Settings.FPS = fps
			m := model.MediaItem{ID: "m", Name: "tone", Path: wav, Kind: model.KindAudio, Duration: 4 * time.Second}
			p.AddMedia(m)
			a2 := p.AddTrack(model.TrackAudio)
			c := p.ClipFromMedia(m, 7, 0)
			c.Length, c.SourceIn = 61, 11
			_ = p.PlaceClip(p.Tracks[2].ID, c)
			c2 := p.ClipFromMedia(m, 23, 0)
			c2.Length, c2.Volume, c2.SourceIn = 45, 0.5, 3
			_ = p.PlaceClip(a2.ID, c2)
			c3 := p.ClipFromMedia(m, 68, 0)
			c3.Length = 13
			_ = p.PlaceClip(p.Tracks[2].ID, c3)
			from, to := 5, 83
			mix, ok := export.BuildAudioMix(p, from, to, 0, nil)
			if !ok {
				t.Fatal("no mix")
			}
			args := []string{"-nostdin", "-loglevel", "error"}
			for _, in := range mix.Inputs {
				args = append(args, "-i", in)
			}
			args = append(args, "-filter_complex", mix.Filter, "-map", "[aout]", "-f", "f32le", "-ar", "48000", "-ac", "2", "-")
			raw, err := exec.Command(ff, args...).Output()
			if err != nil {
				t.Fatal(err)
			}
			want := make([]float32, len(raw)/4)
			_ = binary.Read(bytes.NewReader(raw), binary.LittleEndian, want)
			n := len(want) / 2
			start := FramesToSamples(from, fps)
			if w := FramesToSamples(to, fps) - start; int64(n) != w {
				t.Fatalf("ffmpeg rendered %d frames, want %d", n, w)
			}
			l, r := render(BuildPlan(p, func(model.MediaItem) (Source, error) { return pcm, nil }), start, 1, n)
			bad := 0
			for i := range n {
				if l[i] != want[2*i] || r[i] != want[2*i+1] {
					if bad < 5 {
						t.Errorf("sample %d: engine %v/%v export %v/%v", i, l[i], r[i], want[2*i], want[2*i+1])
					}
					bad++
				}
			}
			if bad > 0 {
				t.Errorf("%d of %d samples differ", bad, n)
			}
		})
	}
}
