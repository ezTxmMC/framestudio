package audio

import (
	"encoding/binary"
	"math"
	"os/exec"
	"path/filepath"
	"testing"

	"framestudio-proto/src/export"
	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
)

// A silent first stream and a non-silent second stream expose accidental fallback
// to stream zero in probing, PCM caching, mixing, waveform generation and export.
func TestMultipleSourceStreamsEndToEnd(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg missing")
	}
	path := filepath.Join(t.TempDir(), "two.mkv")
	args := []string{"-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "color=size=16x16:rate=25:duration=1", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo:d=1", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=1", "-map", "0:v", "-map", "1:a", "-map", "2:a", "-c:v", "ffv1", "-c:a", "pcm_s16le", path}
	if out, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	m, err := media.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.AudioStreams != 2 {
		t.Fatalf("probe streams: %d", m.AudioStreams)
	}
	p := model.NewProject("two")
	old := m
	old.AudioStreams = 0
	p.AddMedia(old)
	m = media.ResolveAudioStreams(p, old)
	if m.AudioStreams != 2 || p.Media[0].AudioStreams != 2 {
		t.Fatal("legacy source stream count not refreshed")
	}
	if _, err := p.PlaceMedia(m, 0, 1, p.Tracks[0].ID); err != nil {
		t.Fatal(err)
	}
	cache := NewCache(nil, nil, 0)
	defer cache.Close()
	for stream := 0; stream < 2; stream++ {
		pcm, err := cache.GetStream(path, stream)
		if err != nil {
			t.Fatal(err)
		}
		<-pcm.Done()
		if pcm.Err() != nil {
			t.Fatal(pcm.Err())
		}
		var energy float64
		for i := int64(0); i < pcm.Frames(); i++ {
			l, r, _ := pcm.Frame(i)
			energy += float64(l*l + r*r)
		}
		if (energy > 0) != (stream == 1) {
			t.Fatalf("stream %d energy %g", stream, energy)
		}
	}
	if len(cache.Keys()) != 2 {
		t.Fatal("PCM streams share cache key")
	}
	waves := media.NewCache()
	defer waves.Close()
	for stream := 0; stream < 2; stream++ {
		item := m
		item.AudioStream = stream
		peaks, err := waves.Waveform(item, 100)
		if err != nil {
			t.Fatal(err)
		}
		var peak float32
		for _, v := range peaks {
			peak = max(peak, v)
		}
		if (peak > 0) != (stream == 1) {
			t.Fatalf("stream %d waveform %g", stream, peak)
		}
	}
	// Solo only stream two. Both live mixing and export must use that stream.
	for i := range p.Tracks {
		if p.Tracks[i].Kind == model.TrackAudio {
			p.Tracks[i].Solo = p.Tracks[i].Clips[0].AudioStream == 1
		}
	}
	pl := BuildPlan(p, func(m model.MediaItem) (Source, error) { return cache.GetStream(m.Path, m.AudioStream) })
	out := make([]float32, 96000)
	tracks := make([][]float32, len(pl.Tracks))
	for i := range tracks {
		tracks[i] = make([]float32, len(out))
	}
	pl.Render(0, 1, Window{}, out, tracks, 48000)
	mix, ok := export.BuildAudioMix(p, 0, 25, 0, nil)
	if !ok {
		t.Fatal("no mix")
	}
	args = []string{"-nostdin", "-loglevel", "error"}
	for _, path := range mix.Inputs {
		args = append(args, "-i", path)
	}
	args = append(args, "-filter_complex", mix.Filter, "-map", "[aout]", "-f", "f32le", "-")
	raw, err := exec.Command(ff, args...).Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != len(out)*4 {
		t.Fatalf("export bytes %d", len(raw))
	}
	var energy float64
	for i, v := range out {
		want := math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
		if math.Abs(float64(v-want)) > 0.00004 {
			t.Fatalf("sample %d live %g export %g", i, v, want)
		}
		energy += float64(v * v)
	}
	if energy == 0 {
		t.Fatal("second stream mix is silent")
	}
}
