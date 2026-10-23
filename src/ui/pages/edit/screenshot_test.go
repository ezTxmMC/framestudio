package edit

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	fx "golang.org/x/image/math/fixed"

	"framestudio-proto/src/assets"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// demoProject is a small but realistic edit: drone shot, interview, still, titles, music.
func demoProject() *model.Project {
	p := model.NewProject("Sommerfilm 2026")
	p.AddTrack(model.TrackAudio)
	add := func(m model.MediaItem) model.MediaItem {
		m.ID = p.AddMedia(m)
		return m
	}
	drone := add(model.MediaItem{Name: "Strand_Drohne.mp4", Kind: model.KindVideo, Duration: 12 * time.Second, Width: 3840, Height: 2160, HasAudio: true})
	inter := add(model.MediaItem{Name: "Interview_Anna.mov", Kind: model.KindVideo, Duration: 20 * time.Second, Width: 1920, Height: 1080, HasAudio: true})
	still := add(model.MediaItem{Name: "Sonnenuntergang.jpg", Kind: model.KindImage, Width: 4000, Height: 2667})
	music := add(model.MediaItem{Name: "Musik_Sommer.wav", Kind: model.KindAudio, Duration: 90 * time.Second})
	add(model.MediaItem{Name: "Logo_Frame_Studio.png", Kind: model.KindImage, Width: 1200, Height: 1200})

	v1, v2, a1, a2 := p.Tracks[0].ID, p.Tracks[1].ID, p.Tracks[2].ID, p.Tracks[3].ID
	place := func(track string, m model.MediaItem, start, length int, kind model.ClipKind) {
		c := p.ClipFromMedia(m, start, length)
		c.Length = length
		if kind == model.KindAudio {
			c.Kind, c.Color = kind, audioClipColor
		}
		_ = p.PlaceClip(track, c)
	}
	place(v1, drone, 0, 250, model.KindVideo)
	place(a1, drone, 0, 250, model.KindAudio)
	place(v1, inter, 250, 350, model.KindVideo)
	place(a1, inter, 250, 350, model.KindAudio)
	place(v1, still, 600, 125, model.KindImage)
	place(a2, music, 0, 725, model.KindAudio)

	title := model.NewClip(model.KindTitle, "Haupttitel", 25, 150)
	title.Title.Text = "Sommer am Meer"
	title.Title.Size = 0.12
	title.FadeIn, title.FadeOut = 12, 12
	_ = p.PlaceClip(v2, title)
	lower := generatorClip("lower", 25, 300, 5)
	lower.Title.Text = "Anna Berger · Regie"
	_ = p.PlaceClip(v2, lower)
	p.AddMarker(250, "Interview")
	p.AddMarker(600, "Schluss")
	return p
}

var demoFont *opentype.Font

func init() {
	demoFont, _ = opentype.Parse(assets.FontBold)
}

// fakeFrame stands in for the renderer: a sunset landscape plus the active titles.
func fakeFrame(p *model.Project, f, w, h int, _ render.Options) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	lerp := func(a, b color.NRGBA, t float64) color.NRGBA {
		m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
		return color.NRGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), 255}
	}
	sky0, sky1 := color.NRGBA{0x1F, 0x3A, 0x5F, 255}, color.NRGBA{0xE8, 0xA0, 0x62, 255}
	sea0, sea1 := color.NRGBA{0x2A, 0x3F, 0x55, 255}, color.NRGBA{0x0C, 0x16, 0x22, 255}
	horizon := 0.6
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		for x := 0; x < w; x++ {
			var c color.NRGBA
			if t < horizon {
				c = lerp(sky0, sky1, math.Pow(t/horizon, 1.6))
			} else {
				c = lerp(sea0, sea1, (t-horizon)/(1-horizon))
				if dx := math.Abs(float64(x)/float64(w) - 0.68); dx < 0.05*(1-dx*8) && y%5 < 2 {
					c = lerp(c, color.NRGBA{0xF4, 0xC0, 0x80, 255}, 0.55)
				}
			}
			cx, cy := float64(x)-0.68*float64(w), float64(y)-0.57*float64(h)
			if d := math.Hypot(cx, cy) / (0.07 * float64(h)); d < 1 && t < horizon {
				c = lerp(c, color.NRGBA{0xFF, 0xDA, 0x9A, 255}, 1-d*d*0.3)
			}
			img.SetNRGBA(x, y, c)
		}
	}
	for _, ti := range p.TrackIndices(model.TrackVideo) {
		for _, c := range p.Tracks[ti].Clips {
			if c.Kind == model.KindTitle && c.Contains(f) {
				drawTitle(img, c.Title)
			}
		}
	}
	return img
}

func drawTitle(img *image.NRGBA, t model.Title) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	face, err := opentype.NewFace(demoFont, &opentype.FaceOptions{Size: t.Size * float64(h), DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return
	}
	defer face.Close()
	d := &font.Drawer{Dst: img, Src: image.NewUniform(t.Color.NRGBA()), Face: face}
	adv := d.MeasureString(t.Text).Round()
	m := face.Metrics()
	x := (w - adv) / 2
	if t.Align == model.AlignLeft {
		x = int(0.08 * float64(w))
	}
	base := int(t.Y*float64(h)) + (m.Ascent.Round()-m.Descent.Round())/2
	if t.Background.A > 0 {
		pad := int(t.Size * float64(h) * 0.45)
		r := image.Rect(x-pad, base-m.Ascent.Round()-pad/2, x+adv+pad, base+m.Descent.Round()+pad/2)
		draw.Draw(img, r, image.NewUniform(t.Background.NRGBA()), image.Point{}, draw.Over)
	}
	d.Dot = fx.P(x, base)
	d.DrawString(t.Text)
}

// fakeTimeline is a static stand-in for the timeline widget during screenshots.
type fakeTimeline struct {
	widget.BaseWidget
	p        *model.Project
	playhead int
	selected string
}

func (t *fakeTimeline) CreateRenderer() fyne.WidgetRenderer { return &fakeTimelineRenderer{t: t} }

type fakeTimelineRenderer struct {
	t    *fakeTimeline
	objs []fyne.CanvasObject
}

func (r *fakeTimelineRenderer) Layout(size fyne.Size) {
	p := r.t.p
	r.objs = nil
	add := func(o fyne.CanvasObject, x, y, w, h float32) {
		o.Move(fyne.NewPos(x, y))
		o.Resize(fyne.NewSize(w, h))
		r.objs = append(r.objs, o)
	}
	rect := func(c color.Color) *canvas.Rectangle { return canvas.NewRectangle(c) }
	const head, ruler, laneH = 112, 26, 46
	scale := (size.Width - head - 20) / float32(p.Duration()+40)
	xOf := func(f int) float32 { return head + float32(f)*scale }

	add(rect(studiotheme.Lane), 0, 0, size.Width, size.Height)
	add(rect(studiotheme.Raised), head, 0, size.Width-head, ruler)
	for s := 0; s*25 <= p.Duration()+40; s++ {
		x := xOf(s * 25)
		tick := float32(6)
		if s%5 == 0 {
			tick = 10
			add(kit.MonoText(model.Timecode(s*25, 25), studiotheme.Muted, 10), x+3, 2, 80, 12)
		}
		add(rect(studiotheme.Faint), x, ruler-tick, 1, tick)
	}
	order := append(append([]int{}, reverse(p.TrackIndices(model.TrackVideo))...), p.TrackIndices(model.TrackAudio)...)
	for i, ti := range order {
		tr := p.Tracks[ti]
		y := float32(ruler + 2 + i*(laneH+2))
		add(rect(studiotheme.Panel), 0, y, head-2, laneH)
		name := canvas.NewText(tr.Name, studiotheme.Text)
		name.TextSize = 12
		name.TextStyle.Bold = true
		add(name, 10, y+14, 30, 16)
		add(rect(studiotheme.Gap), head, y+laneH, size.Width-head, 2)
		for _, c := range tr.Clips {
			x, w := xOf(c.Start), float32(c.Length)*scale-1
			body := rect(alpha(c.Color.NRGBA(), 0xC8))
			body.CornerRadius = 3
			if c.ID == r.t.selected {
				body.StrokeColor = studiotheme.Steel
				body.StrokeWidth = 2
			}
			add(body, x, y+2, w, laneH-4)
			if c.Kind == model.KindAudio {
				for k := float32(4); k < w-4; k += 3 {
					a := 0.25 + 0.6*math.Abs(math.Sin(float64(k)*0.13)*math.Cos(float64(k)*0.031))
					bh := float32(a) * (laneH - 22)
					add(rect(color.NRGBA{0, 0, 0, 0x50}), x+k, y+laneH/2+6-bh/2, 2, bh)
				}
			}
			label := canvas.NewText(c.Name, studiotheme.Text)
			label.TextSize = 11
			add(label, x+6, y+4, w-8, 14)
		}
	}
	for _, m := range p.Markers {
		mk := rect(m.Color.NRGBA())
		add(mk, xOf(m.Frame)-4, 2, 8, 8)
	}
	px := xOf(r.t.playhead)
	add(rect(studiotheme.Tally), px, 0, 1, size.Height)
	headTop := rect(studiotheme.Tally)
	headTop.CornerRadius = 2
	add(headTop, px-5, 0, 11, 12)
}

func reverse(a []int) []int {
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
	return a
}

func (r *fakeTimelineRenderer) MinSize() fyne.Size           { return fyne.NewSize(200, 120) }
func (r *fakeTimelineRenderer) Refresh()                     { r.Layout(r.t.Size()) }
func (r *fakeTimelineRenderer) Destroy()                     {}
func (r *fakeTimelineRenderer) Objects() []fyne.CanvasObject { return r.objs }

func capture(t *testing.T, w fyne.Window, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}

func TestScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-edit*.png")
	}
	// As integrated: real (stub) timeline and renderer.
	f := newFixture(t, demoProject())
	f.s.Seek(80)
	f.q.drain()
	capture(t, f.w, "/tmp/fs-edit-stubs.png")

	// With placeholders for the parts other modules deliver.
	f = newFixture(t, demoProject())
	f.pg.viewer.Source = fakeFrame
	title := clipNamed(t, f.s.Project, "Haupttitel")
	f.s.SelectClip(title.ID)
	f.s.Seek(80)
	f.s.SetIn(25)
	f.s.SetOut(600)
	fake := &fakeTimeline{p: f.s.Project, playhead: 80, selected: title.ID}
	fake.ExtendBaseWidget(fake)
	f.pg.timelineHost.Objects = []fyne.CanvasObject{fake}
	f.pg.timelineHost.Refresh()
	f.q.drain()
	f.pg.bar.tips.ShowTip(f.pg.bar.undo)
	capture(t, f.w, "/tmp/fs-edit.png")
	f.pg.bar.tips.HideTip()

	f.s.SelectClip("")
	f.s.Seek(350)
	fake.playhead, fake.selected = 350, ""
	fake.Refresh()
	f.pg.lib.tabs.SelectIndex(1)
	f.q.drain()
	capture(t, f.w, "/tmp/fs-edit-project.png")

	// Every inspector section of a title and an audio clip, unscrolled.
	for _, name := range []string{"Haupttitel", "Musik_Sommer.wav"} {
		f.s.SelectClip(clipNamed(t, f.s.Project, name).ID)
		iw := test.NewTempWindow(t, f.pg.insp.content)
		iw.Resize(fyne.NewSize(330, 1000))
		capture(t, iw, "/tmp/fs-edit-inspector-"+string(name[0])+".png")
		f.w.SetContent(f.pg.root)
	}
}
