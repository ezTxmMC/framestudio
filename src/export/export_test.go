package export

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
}

// testProject is 1 s of 160x90 at 25 fps: a colour clip, a title above it and a sine tone.
func testProject(t *testing.T, seconds int) *model.Project {
	t.Helper()
	wav := filepath.Join(t.TempDir(), "sine.wav")
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi",
		"-i", "sine=frequency=440:duration="+strconv.Itoa(seconds), "-ar", "44100", wav).CombinedOutput()
	if err != nil {
		t.Fatalf("generate wav: %v %s", err, out)
	}
	p := model.NewProject("Test")
	p.Settings.Width, p.Settings.Height = 160, 90
	n := 25 * seconds
	v1, v2, a1 := p.Tracks[0].ID, p.Tracks[1].ID, p.Tracks[2].ID
	must(t, p.PlaceClip(v1, model.NewClip(model.KindColor, "Farbe", 0, n)))
	must(t, p.PlaceClip(v2, model.NewClip(model.KindTitle, "Titel", 5, n-10)))
	m := model.MediaItem{ID: model.NewID(), Name: "sine", Path: wav, Kind: model.KindAudio, Duration: time.Duration(seconds) * time.Second}
	p.AddMedia(m)
	c := p.ClipFromMedia(m, 0, n)
	c.FadeOut = 5
	must(t, p.PlaceClip(a1, c))
	return p
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type probe struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		NbReadFrames string `json:"nb_read_frames"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func ffprobe(t *testing.T, path string) probe {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-show_streams", "-show_format",
		"-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var pr probe
	must(t, json.Unmarshal(out, &pr))
	return pr
}

func (pr probe) duration() float64 {
	d, _ := strconv.ParseFloat(pr.Format.Duration, 64)
	return d
}

func (pr probe) stream(kind string) (int, bool) {
	for i, s := range pr.Streams {
		if s.CodecType == kind {
			return i, true
		}
	}
	return -1, false
}

func TestRunFormats(t *testing.T) {
	needFFmpeg(t)
	p := testProject(t, 1)
	r := render.New(media.NewCache())
	cases := []struct {
		format       Format
		frames       int
		audio, video bool
	}{
		{FormatMP4, 25, true, true},
		{FormatWebM, 25, true, true},
		{FormatGIF, 15, false, true},
		{FormatWAV, 0, true, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.format), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "out"+Extension(tc.format))
			o := Options{Format: tc.format, Path: path, Width: 160, Height: 90, Quality: QualityLow, IncludeAudio: true}
			var last Progress
			calls := 0
			must(t, Run(context.Background(), p, r, o, func(pr Progress) { last = pr; calls++ }))
			if calls == 0 || last.Frame != last.Total {
				t.Errorf("progress: %d calls, last %+v", calls, last)
			}
			pr := ffprobe(t, path)
			if d := pr.duration(); d < 0.9 || d > 1.15 {
				t.Errorf("duration %.3f, want ~1", d)
			}
			vi, hasV := pr.stream("video")
			_, hasA := pr.stream("audio")
			if hasV != tc.video || hasA != tc.audio {
				t.Fatalf("streams video=%v audio=%v, want %v %v", hasV, hasA, tc.video, tc.audio)
			}
			if tc.video {
				s := pr.Streams[vi]
				if s.Width != 160 || s.Height != 90 {
					t.Errorf("size %dx%d", s.Width, s.Height)
				}
				if s.NbReadFrames != strconv.Itoa(tc.frames) {
					t.Errorf("frames %s, want %d", s.NbReadFrames, tc.frames)
				}
			}
			assertNoPartials(t, filepath.Dir(path))
		})
	}
}

func TestRunPNG(t *testing.T) {
	needFFmpeg(t) // for the test tone only
	p := testProject(t, 1)
	dir := filepath.Join(t.TempDir(), "seq")
	o := Options{Format: FormatPNG, Path: dir, Width: 160, Height: 90, From: 5, To: 15}
	must(t, Run(context.Background(), p, render.New(media.NewCache()), o, nil))
	files, _ := filepath.Glob(filepath.Join(dir, "seq_*.png"))
	if len(files) != 10 {
		t.Fatalf("%d files, want 10", len(files))
	}
	f, err := os.Open(filepath.Join(dir, "seq_000009.png"))
	must(t, err)
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	must(t, err)
	if cfg.Width != 160 || cfg.Height != 90 {
		t.Errorf("png size %dx%d", cfg.Width, cfg.Height)
	}
}

func TestRunCancelRemovesOutput(t *testing.T) {
	needFFmpeg(t)
	p := testProject(t, 20)
	for _, f := range []Format{FormatMP4, FormatPNG} {
		t.Run(string(f), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "out"+Extension(f))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			o := Options{Format: f, Path: path, Width: 160, Height: 90, IncludeAudio: true}
			err := Run(ctx, p, nil, o, func(pr Progress) {
				if pr.Frame > 10 {
					cancel()
				}
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Errorf("left behind: %v", entries)
			}
		})
	}
}

func assertNoPartials(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("partial file left: %s", e.Name())
		}
	}
}

func TestValidate(t *testing.T) {
	saved := lookFFmpeg
	defer func() { lookFFmpeg = saved }()
	lookFFmpeg = func() bool { return true }
	p := model.NewProject("V")
	dir := t.TempDir()
	ok := Options{Format: FormatPNG, Path: filepath.Join(dir, "seq"), Width: 160, Height: 90}
	if err := Validate(p, ok); err == nil || !strings.Contains(err.Error(), "Timeline ist leer") {
		t.Errorf("empty timeline: %v", err)
	}
	must(t, p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "c", 0, 10)))
	must(t, Validate(p, ok))

	bad := map[string]Options{
		"gerade Breite":   {Format: FormatMP4, Path: filepath.Join(dir, "a.mp4"), Width: 161, Height: 90},
		"existiert nicht": {Format: FormatMP4, Path: filepath.Join(dir, "nope", "a.mp4"), Width: 160, Height: 90},
		"Auflösung fehlt": {Format: FormatGIF, Path: filepath.Join(dir, "a.gif")},
		"keine hörbaren":  {Format: FormatWAV, Path: filepath.Join(dir, "a.wav")},
		"Unbekanntes":     {Format: "avi", Path: filepath.Join(dir, "a.avi"), Width: 2, Height: 2},
	}
	for want, o := range bad {
		if err := Validate(p, o); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}

	lookFFmpeg = func() bool { return false }
	if err := Validate(p, Options{Format: FormatMP4, Path: filepath.Join(dir, "a.mp4"), Width: 160, Height: 90}); err == nil ||
		!strings.Contains(err.Error(), "ffmpeg wurde nicht gefunden") {
		t.Errorf("missing ffmpeg: %v", err)
	}
	must(t, Validate(p, ok)) // PNG works without ffmpeg
}

func TestPresets(t *testing.T) {
	p := model.NewProject("P")
	p.Settings.Width, p.Settings.Height = 1280, 545
	names := map[string]Options{}
	for _, pr := range Presets(p) {
		names[pr.Name] = pr.Options
	}
	if len(names) != 7 {
		t.Fatalf("%d presets", len(names))
	}
	if o := names["YouTube 1080p (MP4)"]; o.Width != 2538 || o.Height != 1080 || o.Format != FormatMP4 {
		t.Errorf("youtube %+v", o)
	}
	if o := names["Projekt-Auflösung (MP4)"]; o.Width != 1280 || o.Height != 546 {
		t.Errorf("project size must be even: %+v", o)
	}
	if o := names["Nur Audio (WAV)"]; o.Format != FormatWAV || !o.IncludeAudio {
		t.Errorf("wav %+v", o)
	}
}

func audioProject() (*model.Project, []string) {
	p := model.NewProject("A")
	a2 := p.AddTrack(model.TrackAudio).ID
	a1 := p.Tracks[2].ID
	var paths []string
	add := func(track, name string, start, length int, edit func(*model.Clip)) {
		m := model.MediaItem{ID: model.NewID(), Name: name, Path: "/m/" + name, Kind: model.KindAudio, Duration: 10 * time.Second}
		p.AddMedia(m)
		c := p.ClipFromMedia(m, start, length)
		c.Length = length
		if edit != nil {
			edit(&c)
		}
		if err := p.PlaceClip(track, c); err != nil {
			panic(err)
		}
		paths = append(paths, m.Path)
	}
	add(a1, "music", 0, 100, func(c *model.Clip) { c.FadeIn, c.FadeOut, c.Volume = 25, 50, 0.5 })
	add(a2, "voice", 50, 25, func(c *model.Clip) { c.SourceIn = 25 })
	return p, paths
}

func TestBuildAudioMix(t *testing.T) {
	p, _ := audioProject()
	mix, ok := BuildAudioMix(p, 0, 100, 1, nil)
	if !ok || len(mix.Inputs) != 2 {
		t.Fatalf("mix %+v", mix)
	}
	want := "[1:a:0]aresample=async=1:first_pts=0,aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo,atrim=start_sample=0:end_sample=192000,asetpts=PTS-STARTPTS,apad=whole_len=192000," +
		"afade=t=in:ss=0:ns=48000,afade=t=out:ss=96000:ns=96000,volume=0.5[a0];" +
		"[2:a:0]aresample=async=1:first_pts=0,aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo,atrim=start_sample=48000:end_sample=96000,asetpts=PTS-STARTPTS,apad=whole_len=48000," +
		"adelay=delays=96000S:all=1,asetpts=N/SR/TB[a1];" +
		"[a0][a1]amix=inputs=2:duration=longest:dropout_transition=0:normalize=0,apad=whole_len=192000,atrim=end_sample=192000,asetpts=PTS-STARTPTS[aout]"
	if mix.Filter != want {
		t.Errorf("filter\n got %s\nwant %s", mix.Filter, want)
	}

	// Range starting inside the first clip trims after the fades and shifts the delay.
	mix, _ = BuildAudioMix(p, 40, 60, 0, nil)
	if !strings.Contains(mix.Filter, "afade=t=out:ss=96000:ns=96000,atrim=start_sample=76800:end_sample=115200,asetpts=PTS-STARTPTS,volume=0.5[a0]") ||
		!strings.Contains(mix.Filter, "atrim=start_sample=0:end_sample=19200,asetpts=PTS-STARTPTS,adelay=delays=19200S:all=1,asetpts=N/SR/TB[a1]") ||
		!strings.HasSuffix(mix.Filter, "apad=whole_len=38400,atrim=end_sample=38400,asetpts=PTS-STARTPTS[aout]") {
		t.Errorf("range filter: %s", mix.Filter)
	}

	// Track volume multiplies clip volume.
	p.Tracks[2].Volume = 0.5
	mix, _ = BuildAudioMix(p, 0, 100, 0, nil)
	if !strings.Contains(mix.Filter, "volume=0.25[a0]") {
		t.Errorf("track volume: %s", mix.Filter)
	}

	// Mute drops a track; a single input needs no amix.
	p.Tracks[2].Muted = true
	mix, _ = BuildAudioMix(p, 0, 100, 0, nil)
	if len(mix.Inputs) != 1 || mix.Inputs[0] != "/m/voice" || strings.Contains(mix.Filter, "amix") {
		t.Errorf("mute: %+v", mix)
	}

	// Solo plays only solo tracks, even over an unmuted one.
	p.Tracks[2].Muted = false
	p.Tracks[2].Solo = true
	mix, _ = BuildAudioMix(p, 0, 100, 0, nil)
	if len(mix.Inputs) != 1 || mix.Inputs[0] != "/m/music" {
		t.Errorf("solo: %+v", mix.Inputs)
	}

	// Silence and unavailable media produce no mix.
	p.Tracks[2].Volume = 0
	if _, ok := BuildAudioMix(p, 0, 100, 0, nil); ok {
		t.Error("zero volume should be silent")
	}
	p.Tracks[2].Volume = 1
	if _, ok := BuildAudioMix(p, 0, 100, 0, func(model.MediaItem) bool { return false }); ok {
		t.Error("unavailable media should be skipped")
	}
	if _, ok := BuildAudioMix(p, 200, 300, 0, nil); ok {
		t.Error("range without clips should be silent")
	}
}

func TestAudioFromVideoMedia(t *testing.T) {
	p := model.NewProject("V")
	m := model.MediaItem{ID: "v", Path: "/m/clip.mp4", Kind: model.KindVideo, Duration: time.Second}
	p.AddMedia(m)
	c := p.ClipFromMedia(m, 0, 25)
	c.Kind = model.KindAudio
	must(t, p.PlaceClip(p.Tracks[2].ID, c))
	if HasAudio(p, 0, 25) {
		t.Error("video without sound must not be mixed")
	}
	p.Media[0].HasAudio = true
	if !HasAudio(p, 0, 25) {
		t.Error("video sound track should be mixed")
	}
}

func TestPipelineOrder(t *testing.T) {
	var got []int
	err := pipeline(context.Background(), 200, 8,
		func(i int) (int, error) {
			time.Sleep(time.Duration((i*7)%5) * 100 * time.Microsecond)
			return i * 2, nil
		},
		func(i, v int) error {
			if v != i*2 {
				t.Errorf("slot %d got %d", i, v)
			}
			got = append(got, i)
			return nil
		})
	must(t, err)
	for i, v := range got {
		if v != i {
			t.Fatalf("out of order at %d: %d", i, v)
		}
	}
	if len(got) != 200 {
		t.Fatalf("%d results", len(got))
	}
	boom := errors.New("boom")
	err = pipeline(context.Background(), 100, 4,
		func(i int) (int, error) {
			if i == 30 {
				return 0, boom
			}
			return i, nil
		},
		func(int, int) error { return nil })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestTimelineFrames(t *testing.T) {
	if got := timelineFrames(10, 35, 25, 15); len(got) != 15 || got[0] != 10 || got[1] != 11 || got[14] != 33 {
		t.Errorf("gif sampling %v", got)
	}
	if got := timelineFrames(0, 3, 25, 25); len(got) != 3 || got[2] != 2 {
		t.Errorf("full rate %v", got)
	}
}

// fakeReader yields frames whose width encodes the source frame number.
type fakeReader struct{ n, end int }

func (f *fakeReader) Next() (image.Image, error) {
	if f.n >= f.end {
		return nil, io.EOF
	}
	f.n++
	return image.NewNRGBA(image.Rect(0, 0, f.n, 1)), nil
}
func (f *fakeReader) Close() error { return nil }

type fakeFallback struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeFallback) VideoFrame(m model.MediaItem, n, fps, w, h int) (image.Image, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return image.NewNRGBA(image.Rect(0, 0, n+1, 1)), nil
}

func TestSeqSource(t *testing.T) {
	fb := &fakeFallback{}
	s := newSeqSource(fb, 4)
	opens := 0
	s.open = func(m model.MediaItem, n, fps, w, h int) (media.FrameReader, error) {
		opens++
		return &fakeReader{n: n, end: 100}, nil
	}
	m := model.MediaItem{ID: "m", Path: "/m"}
	check := func(n int) {
		t.Helper()
		img, err := s.VideoFrame(m, n, 25, 8, 8)
		if err != nil || img.Bounds().Dx() != n+1 {
			t.Fatalf("frame %d: %v %v", n, img.Bounds(), err)
		}
	}
	for n := 10; n < 40; n++ {
		check(n)
	}
	check(38) // slightly behind: served from the ring
	check(45) // short skip ahead
	if opens != 1 || fb.calls != 0 {
		t.Errorf("sequential: opens=%d fallback=%d", opens, fb.calls)
	}
	check(5) // jump back: second decoder
	if opens != 2 {
		t.Errorf("jump back opens=%d", opens)
	}
	check(99)
	check(100) // past the end of the reader: fallback
	if fb.calls != 1 {
		t.Errorf("fallback calls %d", fb.calls)
	}
	s.Close()
	check(50)
	if fb.calls != 2 {
		t.Errorf("closed source should fall back")
	}

	// Concurrent, slightly reordered access stays correct.
	s = newSeqSource(fb, 8)
	s.open = func(m model.MediaItem, n, fps, w, h int) (media.FrameReader, error) {
		return &fakeReader{n: n, end: 1000}, nil
	}
	err := pipeline(context.Background(), 300, 6, func(i int) (int, error) {
		img, err := s.VideoFrame(m, i, 25, 8, 8)
		if err != nil {
			return 0, err
		}
		return img.Bounds().Dx(), nil
	}, func(i, w int) error {
		if w != i+1 {
			t.Errorf("frame %d got %d", i, w-1)
		}
		return nil
	})
	must(t, err)
	s.Close()
}

// soundSpans returns the [start,end) seconds of non-silent audio in a file.
func soundSpans(t *testing.T, path string, total float64) [][2]float64 {
	t.Helper()
	out, _ := exec.Command("ffmpeg", "-hide_banner", "-nostats", "-i", path, "-af", "silencedetect=n=-40dB:d=0.2",
		"-f", "null", "-").CombinedOutput()
	var spans [][2]float64
	start := 0.0
	for _, line := range strings.Split(string(out), "\n") {
		if _, v, ok := strings.Cut(line, "silence_start: "); ok {
			s, _ := strconv.ParseFloat(strings.Fields(v)[0], 64)
			if s > start+0.01 {
				spans = append(spans, [2]float64{start, s})
			}
		}
		if _, v, ok := strings.Cut(line, "silence_end: "); ok {
			start, _ = strconv.ParseFloat(strings.Fields(v)[0], 64)
		}
	}
	if start < total-0.05 && (!strings.Contains(string(out), "silence_start") ||
		strings.LastIndex(string(out), "silence_end") > strings.LastIndex(string(out), "silence_start")) {
		spans = append(spans, [2]float64{start, total}) // sound runs to the end
	}
	return spans
}

func TestAudioOffsetWithSourceIn(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	tone := filepath.Join(dir, "tone.wav")
	out, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=6", tone).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	p := model.NewProject("Sync")
	a2 := p.AddTrack(model.TrackAudio).ID
	m := model.MediaItem{ID: "tone", Name: "tone", Path: tone, Kind: model.KindAudio, Duration: 6 * time.Second}
	p.AddMedia(m)
	c1 := p.ClipFromMedia(m, 25, 0)
	c1.SourceIn, c1.Length = 10, 50 // 1 s .. 3 s, starts inside the source
	must(t, p.PlaceClip(p.Tracks[2].ID, c1))
	c2 := p.ClipFromMedia(m, 100, 0)
	c2.Length = 100 // 4 s .. 8 s
	c2.FadeIn, c2.FadeOut = 10, 60
	must(t, p.PlaceClip(a2, c2))
	// cuts inside the fade-out give partial ramps (afade silence/unity)
	right, err := p.SplitClip(c2.ID, 160)
	must(t, err)
	_, err = p.SplitClip(right, 180)
	must(t, err)
	if mix, _ := BuildAudioMix(p, 0, 200, 0, nil); !strings.Contains(mix.Filter, ":silence=") || !strings.Contains(mix.Filter, ":unity=") {
		t.Errorf("split fades need partial ramps: %s", mix.Filter)
	}
	path := filepath.Join(dir, "mix.wav")
	must(t, Run(context.Background(), p, nil, Options{Format: FormatWAV, Path: path, IncludeAudio: true}, nil))
	d := ffprobe(t, path).duration()
	if d < 7.95 || d > 8.05 {
		t.Errorf("mixdown is %.3f s, want 8", d)
	}
	spans := soundSpans(t, path, d)
	want := [][2]float64{{1, 3}, {4, 7.9}}
	if len(spans) != 2 {
		t.Fatalf("sound at %v, want %v", spans, want)
	}
	for i := range want {
		if d := spans[i][0] - want[i][0]; d < -0.06 || d > 0.06 {
			t.Errorf("span %d starts at %.3f s, want %.0f", i, spans[i][0], want[i][0])
		}
	}
	if d := spans[0][1] - 3; d < -0.06 || d > 0.06 {
		t.Errorf("first span ends at %.3f s, want 3", spans[0][1])
	}
}

func TestValidateReportsOfflineMedia(t *testing.T) {
	saved := lookFFmpeg
	defer func() { lookFFmpeg = saved }()
	lookFFmpeg = func() bool { return true }
	p := model.NewProject("Offline")
	v := model.MediaItem{ID: "v", Name: "Strand", Path: "/fehlt/strand.mp4", Kind: model.KindVideo, Duration: 2 * time.Second, HasAudio: true}
	a := model.MediaItem{ID: "a", Name: "Musik", Path: "/fehlt/musik.wav", Kind: model.KindAudio, Duration: 2 * time.Second}
	p.AddMedia(v)
	p.AddMedia(a)
	must(t, p.PlaceClip(p.Tracks[0].ID, p.ClipFromMedia(v, 0, 0)))
	must(t, p.PlaceClip(p.Tracks[2].ID, p.ClipFromMedia(a, 0, 0)))
	dir := t.TempDir()
	o := Options{Format: FormatMP4, Path: filepath.Join(dir, "a.mp4"), Width: 160, Height: 90, IncludeAudio: true}
	err := Validate(p, o)
	var off *OfflineError
	if !errors.As(err, &off) || len(off.Media) != 2 || !strings.Contains(err.Error(), "Strand, Musik") {
		t.Fatalf("got %v", err)
	}
	o.IncludeAudio = false
	if err := Validate(p, o); !errors.As(err, &off) || len(off.Media) != 1 {
		t.Fatalf("without sound only the picture counts: %v", err)
	}
	o.AllowOffline = true
	must(t, Validate(p, o))
}

func TestPNGReplacesOldSequenceSafely(t *testing.T) {
	needFFmpeg(t)
	p := testProject(t, 2)
	dir := filepath.Join(t.TempDir(), "seq")
	o := Options{Format: FormatPNG, Path: dir, Width: 160, Height: 90, From: 0, To: 30}
	must(t, Run(context.Background(), p, nil, o, nil))
	keep := filepath.Join(dir, "notiz.txt")
	must(t, os.WriteFile(keep, []byte("x"), 0o644))

	// A cancelled re-export leaves the earlier sequence untouched.
	ctx, cancel := context.WithCancel(context.Background())
	o2 := o
	o2.To = 50
	err := Run(ctx, p, nil, o2, func(pr Progress) {
		if pr.Frame > 3 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "seq_*.png"))
	if len(files) != 30 {
		t.Fatalf("%d frames after cancel, want the 30 of the earlier export", len(files))
	}

	// A shorter export replaces the sequence without leaving stale frames.
	o3 := o
	o3.To = 10
	must(t, Run(context.Background(), p, nil, o3, nil))
	files, _ = filepath.Glob(filepath.Join(dir, "seq_*.png"))
	if len(files) != 10 {
		t.Fatalf("%d frames, want 10", len(files))
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("unrelated files in the folder must stay")
	}
	assertNoPartials(t, filepath.Dir(dir))
}
