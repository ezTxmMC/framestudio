package edit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/model"
)

var _ interface {
	Dropped([]fyne.URI)
	DroppedAt(fyne.Position, []fyne.URI)
} = (*Page)(nil)

// probeFunc inspects a dropped file (media.ProbeContext); tests replace it.
type probeFunc func(ctx context.Context, path string) (model.MediaItem, error)

// Dropped imports files dragged from the file manager onto the edit page (without a
// known position): they are added to the library and the page stays on Schnitt.
func (p *Page) Dropped(uris []fyne.URI) { p.DroppedAt(fyne.NewPos(-1, -1), uris) }

// DroppedAt imports files dragged from the file manager onto the window at pos (window
// coordinates). Files dropped on the timeline are placed there, at the pointer, like
// media dragged from the library; elsewhere they only join the library. Probing runs
// in the background; the page stays on Schnitt.
func (p *Page) DroppedAt(pos fyne.Position, uris []fyne.URI) {
	if p.closed || p.root == nil {
		return
	}
	var paths []string
	folders := 0
	for _, u := range uris {
		if u == nil || u.Scheme() != "file" {
			continue
		}
		if st, err := os.Stat(u.Path()); err == nil && st.IsDir() {
			folders++
			continue
		}
		paths = append(paths, u.Path())
	}
	if folders > 0 && len(paths) == 0 {
		p.showErr(errors.New("Ordner lassen sich auf der Seite Medien importieren. Medien › Ordner importieren wählen oder die Dateien selbst hineinziehen"))
		return
	}
	if len(paths) == 0 {
		return
	}
	at, onTimeline := p.timelinePoint(pos)
	probe, life := p.probe, p.lifeCtx()
	p.spawn(func() {
		items := make([]model.MediaItem, len(paths))
		errs := make([]error, len(paths))
		for i, path := range paths {
			if life.Err() != nil {
				return
			}
			items[i], errs[i] = probe(life, path)
		}
		p.post(func() { p.finishDrop(items, errs, at, onTimeline) })
	})
}

// timelinePoint converts a window position to timeline coordinates; ok is false when it
// is not over the timeline.
func (p *Page) timelinePoint(pos fyne.Position) (fyne.Position, bool) {
	if pos.X < 0 || p.tl == nil {
		return fyne.Position{}, false
	}
	app := fyne.CurrentApp()
	if app == nil {
		return fyne.Position{}, false
	}
	origin := app.Driver().AbsolutePositionForObject(p.tl)
	rel := pos.Subtract(origin)
	sz := p.tl.Size()
	if !p.tl.Visible() || rel.X < 0 || rel.Y < 0 || rel.X > sz.Width || rel.Y > sz.Height {
		return fyne.Position{}, false
	}
	return rel, true
}

// finishDrop adds the probed files to the project as one undo step (files already in
// the project are reused) and places them on the timeline when they were dropped there.
func (p *Page) finishDrop(items []model.MediaItem, errs []error, at fyne.Position, onTimeline bool) {
	if p.closed {
		return
	}
	s := p.ctx.Session
	var ids, failed []string
	var add []model.MediaItem
	known := map[string]string{}
	for _, m := range s.Project.Media {
		known[m.Path] = m.ID
	}
	for i, err := range errs {
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}
		if id, ok := known[items[i].Path]; ok {
			ids = append(ids, id)
			continue
		}
		known[items[i].Path] = items[i].ID
		add = append(add, items[i])
		ids = append(ids, items[i].ID)
	}
	if len(add) > 0 {
		label := "Medium importieren"
		if len(add) > 1 {
			label = fmt.Sprintf("%d Medien importieren", len(add))
		}
		if err := p.edit(label, func(pr *model.Project) error {
			for _, m := range add {
				pr.AddMedia(m)
			}
			return nil
		}); err != nil {
			return
		}
	}
	if len(ids) > 0 {
		s.SelectMedia(ids[len(ids)-1])
		if onTimeline {
			p.tl.MediaDrop(ids, at)
		}
	}
	if len(failed) > 0 {
		p.showErr(fmt.Errorf("%d von %d Dateien nicht importiert: %s", len(failed), len(errs), strings.Join(failed, "; ")))
	}
}

// lifeCtx returns the context that ends when the page closes.
func (p *Page) lifeCtx() context.Context {
	if p.life == nil {
		p.life, p.stopLife = context.WithCancel(context.Background())
	}
	return p.life
}
