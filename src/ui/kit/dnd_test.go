package kit

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

type zone struct {
	widget.Label
	over    []string
	dropped fyne.Position
	left    bool
}

func newZone() *zone {
	z := &zone{}
	z.ExtendBaseWidget(z)
	return z
}

func (z *zone) MinSize() fyne.Size                           { return fyne.NewSize(100, 100) }
func (z *zone) MediaDragOver(ids []string, _ fyne.Position) { z.over = ids }
func (z *zone) MediaDragLeave()                              { z.left = true }
func (z *zone) MediaDrop(ids []string, p fyne.Position) bool { z.dropped = p; return true }

func TestMediaDragDrop(t *testing.T) {
	test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	z := newZone()
	w.SetContent(container.NewHBox(layout.NewSpacer(), z))
	w.Resize(fyne.NewSize(300, 120))
	unreg := RegisterDropZone(w, z)
	defer unreg()

	zpos := fyne.CurrentApp().Driver().AbsolutePositionForObject(z)
	BeginMediaDrag(w, []string{"m1"}, "Clip")
	UpdateMediaDrag(fyne.NewPos(5, 5))
	if z.over != nil {
		t.Fatal("pointer outside the zone must not hover it")
	}
	UpdateMediaDrag(zpos.Add(fyne.NewPos(20, 30)))
	if len(z.over) != 1 {
		t.Fatal("hover not reported")
	}
	if !EndMediaDrag(zpos.Add(fyne.NewPos(20, 30))) || z.dropped != fyne.NewPos(20, 30) || MediaDragActive() {
		t.Fatalf("drop not delivered at relative position: %v", z.dropped)
	}
	z.Hide()
	BeginMediaDrag(w, []string{"m1"}, "Clip")
	if EndMediaDrag(zpos.Add(fyne.NewPos(20, 30))) {
		t.Fatal("hidden zone must not accept drops")
	}
}
