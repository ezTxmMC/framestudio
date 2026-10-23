package render

import (
	"context"
	"image"
	"image/color"
	"testing"

	"framestudio-proto/src/model"
)

// videoProject has a video layer (served by fakeSource as RGBA) with a grade under a
// half-transparent colour clip.
func videoProject(t *testing.T) (*model.Project, *Renderer) {
	t.Helper()
	p := newProject(t)
	id := p.AddMedia(model.MediaItem{Name: "clip.mp4", Path: "/nirgends/clip.mp4", Kind: model.KindVideo, Width: 1920, Height: 1080})
	v := model.NewClip(model.KindVideo, "clip", 0, 100)
	v.MediaID = id
	v.Grade = model.Grade{Contrast: 0.2, Saturation: 0.1}
	place(t, p, 0, v)
	top := colorClip(blue, 0, 100)
	top.Opacity = 0.5
	place(t, p, 1, top)
	var cached *image.RGBA // the decoder owns its frames; the test must not allocate per call
	src := &fakeSource{video: func(n, w, h int) image.Image {
		if cached != nil && cached.Rect.Dx() == w && cached.Rect.Dy() == h {
			return cached
		}
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(n+i), uint8(i>>3), 90, 255
		}
		cached = img
		return img
	}}
	return p, &Renderer{Video: src}
}

func TestRenderIntoMatchesFrameWith(t *testing.T) {
	p, r := videoProject(t)
	want := r.FrameWith(p, 7, 192, 108, Options{})
	dst := image.NewRGBA(image.Rect(0, 0, 192, 108))
	info, err := r.RenderInto(context.Background(), dst, p, 7, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !info.Opaque || info.Layers != 2 {
		t.Fatalf("info %+v", info)
	}
	// opaque result: premultiplied == straight
	for y := 0; y < 108; y++ {
		for x := 0; x < 192; x++ {
			w := want.NRGBAAt(x, y)
			g := dst.RGBAAt(x, y)
			if (color.RGBA{w.R, w.G, w.B, w.A}) != g {
				t.Fatalf("pixel (%d,%d): RenderInto %v, FrameWith %v", x, y, g, w)
			}
		}
	}
}

func TestRenderIntoPremultipliesTransparent(t *testing.T) {
	p := newProject(t)
	p.Settings.Background = rgba(200, 100, 0, 128)
	r := &Renderer{}
	dst := image.NewRGBA(image.Rect(0, 0, 8, 8))
	info, err := r.RenderInto(context.Background(), dst, p, 0, Options{})
	if err != nil || info.Opaque {
		t.Fatalf("info %+v err %v", info, err)
	}
	if g := dst.RGBAAt(1, 1); g.A != 128 || g.R != 100 || g.G != 50 {
		t.Fatalf("premultiplied pixel %v", g)
	}
}

func TestRenderIntoCancelled(t *testing.T) {
	p, r := videoProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dst := image.NewRGBA(image.Rect(0, 0, 64, 36))
	if _, err := r.RenderInto(ctx, dst, p, 0, Options{}); err == nil {
		t.Fatal("cancelled context must abort the render")
	}
}

// Steady-state allocation of a 1080p three-layer render stays far below the 26 MB per
// frame measured before the scratch pools (budget: 2 MB).
func TestRenderIntoAllocBudget(t *testing.T) {
	p, r := videoProject(t)
	p.Tracks = append(p.Tracks, model.Track{ID: "v3", Name: "V3", Kind: model.TrackVideo, Volume: 1})
	g := colorClip(green, 0, 100)
	g.Opacity = 0.3
	place(t, p, len(p.Tracks)-1, g)
	dst := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	ctx := context.Background()
	for range 3 { // warm the pools
		_, _ = r.RenderInto(ctx, dst, p, 3, Options{})
	}
	var before, after runtimeMem
	before.read()
	const n = 10
	for i := range n {
		_, _ = r.RenderInto(ctx, dst, p, 3+i, Options{})
	}
	after.read()
	per := (after.total - before.total) / n
	t.Logf("%d bytes allocated per 1080p frame", per)
	if per > 2<<20 {
		t.Fatalf("%d bytes per frame, want <= 2 MiB", per)
	}
}
