package deliver

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/export"
)

// addJobs queues ready jobs named after names without starting them.
func (h *harness) addJobs(names ...string) []*job {
	var out []*job
	dir := defaultFolder()
	for _, n := range names {
		o := export.Presets(h.s.Project)[0].Options
		o.Path = filepath.Join(dir, n+".mp4")
		out = append(out, h.pg.queue.add(o, h.s.Project.Clone(), false))
	}
	return out
}

func jobNames(q *queue) string {
	var n []string
	for _, j := range q.jobs {
		n = append(n, strings.TrimSuffix(j.name(), ".mp4"))
	}
	return strings.Join(n, ",")
}

func TestQueueReorder(t *testing.T) {
	h := newHarness(t)
	q := h.pg.queue
	js := h.addJobs("A", "B", "C")
	if !q.cards[js[0]].up.Disabled() || q.cards[js[0]].down.Disabled() || !q.cards[js[2]].down.Disabled() {
		t.Fatal("move buttons at the ends must be disabled")
	}
	test.Tap(q.cards[js[2]].up)
	if got := jobNames(q); got != "A,C,B" {
		t.Fatalf("after C up: %s", got)
	}
	if q.list.Objects[1] != q.cards[js[2]].content {
		t.Error("list order not updated")
	}
	test.Tap(q.cards[js[0]].down)
	if got := jobNames(q); got != "C,A,B" {
		t.Fatalf("after A down: %s", got)
	}
	if !q.cards[js[2]].up.Disabled() || q.cards[js[1]].down.Disabled() == false {
		t.Error("enabled states not updated after moving")
	}
	// Render order follows the list: arm A and C, C starts first.
	js[0].status, js[2].status = jobWaiting, jobWaiting
	q.running = nil
	next := func() *job {
		for _, j := range q.jobs {
			if j.status == jobWaiting {
				return j
			}
		}
		return nil
	}
	if next() != js[2] {
		t.Error("the moved-up job should render first")
	}
	// Running and finished jobs cannot be moved.
	js[1].status = jobDone
	q.cards[js[1]].refresh()
	if q.cards[js[1]].order.Visible() {
		t.Error("finished jobs must not show move buttons")
	}
}

func TestClearFinishedKeepsFailedAndQueued(t *testing.T) {
	h := newHarness(t)
	q := h.pg.queue
	js := h.addJobs("Fertig", "Fehler", "Abbruch", "Bereit")
	js[0].status, js[1].status, js[2].status = jobDone, jobFailed, jobCanceled
	js[1].err = errors.New("kaputt")
	for _, j := range js {
		q.cards[j].refresh()
	}
	q.refresh()
	if !q.clear.Visible() || q.clear.Text != "Fertige entfernen" {
		t.Fatalf("clear button %q visible %v", q.clear.Text, q.clear.Visible())
	}
	test.Tap(q.clear)
	if got := jobNames(q); got != "Fehler,Bereit" {
		t.Fatalf("after clearing: %s", got)
	}
	if q.clear.Visible() {
		t.Error("nothing left to clear")
	}
}

func TestFinishedJobNotifies(t *testing.T) {
	h := newHarness(t)
	q := h.pg.queue
	var sent []*fyne.Notification
	h.pg.sendNotification = func(n *fyne.Notification) { sent = append(sent, n) }
	type toast struct {
		title, msg string
		failed     bool
	}
	var toasts []toast
	h.pg.SetToast(func(title, msg string, failed bool) { toasts = append(toasts, toast{title, msg, failed}) })

	js := h.addJobs("Showreel", "Kaputt", "Weg")
	for _, j := range js {
		j.status = jobRunning
		j.cancel = func() {}
	}
	q.finished(js[0], nil, 94*time.Second, 48_300_000)
	if len(sent) != 1 || sent[0].Title != "Export fertig" || !strings.Contains(sent[0].Content, "Showreel.mp4") ||
		!strings.Contains(sent[0].Content, "48,3 MB") {
		t.Fatalf("notification %+v", sent)
	}
	if len(toasts) != 1 || toasts[0].failed || !q.statusBox.Visible() || !strings.Contains(q.status.Text, "Showreel.mp4") {
		t.Fatalf("toast %+v status %q", toasts, q.status.Text)
	}
	q.finished(js[1], errors.New("Kein Speicherplatz"), time.Second, 0)
	if len(sent) != 2 || sent[1].Title != "Export fehlgeschlagen" || !toasts[1].failed ||
		!strings.Contains(q.status.Text, "fehlgeschlagen") {
		t.Fatalf("failure notification %+v / %q", sent, q.status.Text)
	}
	q.finished(js[2], context.Canceled, time.Second, 0)
	if len(sent) != 2 {
		t.Error("a cancelled job must not notify")
	}
}

func TestDefaultNotificationGoesToApp(t *testing.T) {
	h := newHarness(t)
	test.AssertNotificationSent(t, fyne.NewNotification("Export fertig", "x"), func() {
		h.pg.notify("Export fertig", "x", false)
	})
}
