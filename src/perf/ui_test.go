package perf

import (
	"fmt"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	"framestudio-proto/src/ui/pages/edit"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/timeline"
)

// busyProject builds a realistic timeline: 3 video + 2 audio tracks with clipsPer clips each
// (colour/title clips: they cost nothing to render, so the UI side shows up alone).
func busyProject(clipsPer int) *model.Project {
	p := model.NewProject("busy")
	p.Settings.FPS = model.Rate30
	ensureVideoTracks(p, 3)
	p.AddTrack(model.TrackAudio)
	for ti := range p.Tracks {
		t := &p.Tracks[ti]
		for i := 0; i < clipsPer; i++ {
			kind := model.KindColor
			if t.Kind == model.TrackAudio {
				kind = model.KindAudio
			} else if i%5 == 4 {
				kind = model.KindTitle
			}
			c := model.NewClip(kind, fmt.Sprintf("Clip %d.%d", ti, i), i*120, 110)
			if err := p.PlaceClip(t.ID, c); err != nil {
				panic(err)
			}
		}
	}
	return p
}

type editFix struct {
	s     *session.Session
	pg    page.Page
	w     fyne.Window
	q     *kit.UIQueue
	cache *media.Cache
}

func newEdit(t *testing.T, p *model.Project, wd, ht float32) *editFix {
	t.Helper()
	app := test.NewTempApp(t)
	q := kit.UseUIQueue(t)
	app.Settings().SetTheme(studiotheme.New())
	defaults := func() { _ = settings.Update(func(s *settings.Settings) { *s = settings.Defaults() }) }
	defaults()
	t.Cleanup(defaults)
	f := &editFix{s: session.New(p), q: q, cache: media.NewCache()}
	ctx := &page.Context{Session: f.s, Renderer: render.New(f.cache), Media: f.cache,
		Navigate: func(string) {}, ShowErr: func(err error) { t.Log("page error:", err) }}
	reg := command.NewRegistry()
	ctx.Commands = reg
	f.pg = edit.New(ctx)
	f.w = test.NewTempWindow(t, f.pg.Content())
	ctx.Window = f.w
	f.w.Resize(fyne.NewSize(wd, ht))
	f.pg.Activate()
	t.Cleanup(func() { f.pg.Close(); q.Drain(); f.cache.Close() })
	q.Settle(30*time.Millisecond, 2*time.Second)
	return f
}

func countObjects(o fyne.CanvasObject) int {
	n := 1
	switch c := o.(type) {
	case *fyne.Container:
		for _, ch := range c.Objects {
			n += countObjects(ch)
		}
	case fyne.Widget:
		for _, ch := range test.WidgetRenderer(c).Objects() {
			n += countObjects(ch)
		}
	}
	return n
}

// TestPerfUITick: what one playback tick costs on the UI thread with the whole edit page
// subscribed (viewer, transport, toolbar, timeline, inspector, library): s.Advance(33 ms)
// runs every session listener synchronously, as the 60 Hz clock does from kit.DoAndWait.
// Also the cost of the timeline's full rebuild (Refresh), which the follow modes trigger.
func TestPerfUITick(t *testing.T) {
	need(t)
	for _, clips := range []int{20, 200} {
		for _, follow := range []string{"page", "smooth", "off"} {
			f := newEdit(t, busyProject(clips), 1600, 900)
			_ = settings.Update(func(s *settings.Settings) { s.Playback.Follow = follow })
			f.q.Settle(30*time.Millisecond, time.Second)
			f.s.Seek(0)
			f.s.SetPlaying(true)
			var tick, drain stats
			nd := 0
			const N = 240
			for i := 0; i < N; i++ {
				a := time.Now()
				f.s.Advance(time.Second / 30)
				tick.add(time.Since(a))
				time.Sleep(2 * time.Millisecond) // lets the render goroutine post its frame
				b := time.Now()
				nd += f.q.Drain()
				drain.add(time.Since(b))
			}
			ph := f.s.Playhead
			f.s.SetPlaying(false)
			t.Logf("playhead=%d; UI callbacks run=%d", ph, nd)
			t.Logf("UITICK clips/track=%-3d follow=%-6s objects=%d  Advance(): %s\n      drain of posted frame callbacks: %s",
				clips, follow, countObjects(f.pg.Content()), tick.String(), drain.String())
		}
	}
}

// TestPerfTimelineRebuild: cost of one full timeline Refresh (rebuild of every lane object),
// the price of FollowSmooth on every frame and of any project change during playback.
func TestPerfTimelineRebuild(t *testing.T) {
	need(t)
	for _, clips := range []int{20, 200} {
		f := newEdit(t, busyProject(clips), 1600, 900)
		var st stats
		for i := 0; i < 60; i++ {
			a := time.Now()
			f.pg.Content().Refresh()
			st.add(time.Since(a))
		}
		var tl stats
		for i := 0; i < 60; i++ {
			f.s.Seek(i)
			a := time.Now()
			f.w.Canvas().Content().Refresh()
			tl.add(time.Since(a))
		}
		t.Logf("TLREBUILD clips/track=%d page.Content().Refresh(): %s", clips, st.String())
	}
}

func videoProject(tb testing.TB, clipsPer int) *model.Project {
	p := model.NewProject("busyvideo")
	p.Settings.FPS = model.Rate30
	ensureVideoTracks(p, 3)
	m := probe(tb, H264_1080p30)
	p.Media = append(p.Media, m)
	for _, ti := range p.TrackIndices(model.TrackVideo) {
		for i := 0; i < clipsPer; i++ {
			c := model.NewClip(model.KindVideo, fmt.Sprintf("Video %d", i), i*100, 90)
			c.MediaID = m.ID
			c.SourceIn = (i * 7) % 100
			if err := p.PlaceClip(p.Tracks[ti].ID, c); err != nil {
				panic(err)
			}
		}
	}
	return p
}

// TestPerfTimelineFollow: per-tick UI cost of the stand-alone timeline while zoomed in,
// for the three follow modes. FollowSmooth moves the view on every frame, which goes
// through t.Refresh() = full rebuild of all lane objects; FollowPage rebuilds only when
// the playhead leaves the view.
func TestPerfTimelineFollow(t *testing.T) {
	need(t)
	for _, video := range []bool{false, true} {
		for _, follow := range []string{"page", "smooth", "off"} {
			app := test.NewTempApp(t)
			q := kit.UseUIQueue(t)
			app.Settings().SetTheme(studiotheme.New())
			var p *model.Project
			if video {
				p = videoProject(t, 60)
			} else {
				p = busyProject(200)
			}
			s := session.New(p)
			cache := media.NewCache()
			tl := timeline.New(s, cache)
			tl.SetFollow(follow)
			w := test.NewTempWindow(t, tl)
			w.Resize(fyne.NewSize(1600, 400))
			tl.SetZoom(8)
			q.Settle(100*time.Millisecond, 4*time.Second) // thumbnails/waveforms load
			s.Seek(4000)
			s.SetPlaying(true)
			var tick stats
			g0 := readGC()
			for i := 0; i < 300; i++ {
				a := time.Now()
				s.Advance(time.Second / 30)
				tick.add(time.Since(a))
				q.Drain()
				time.Sleep(time.Millisecond)
			}
			g := readGC().since(g0)
			s.SetPlaying(false)
			t.Logf("TLFOLLOW video=%v follow=%-6s objects=%d Advance(): %s\n      %s", video, follow, countObjects(tl), tick.String(), g.String())
			tl.Close()
			cache.Close()
		}
	}
}
