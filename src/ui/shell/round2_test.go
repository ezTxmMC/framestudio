package shell

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
)

// typeShortcut delivers sc like the window canvas does.
func typeShortcut(t *testing.T, sh *Shell, sc fyne.Shortcut) {
	t.Helper()
	fn := sh.shortcuts[sc.ShortcutName()]
	if fn == nil {
		t.Fatalf("no window shortcut %s", sc.ShortcutName())
	}
	fn()
}

func TestPageShortcuts(t *testing.T) {
	sh, _, made := newTestShell(t)
	sh.Start([]string{saveSample(t, "Seiten", 1920, 1080, 25, 100)})
	cases := []struct {
		mod  fyne.KeyModifier
		key  fyne.KeyName
		want string
	}{
		{fyne.KeyModifierShift, fyne.Key3, page.Color},
		{fyne.KeyModifierAlt, fyne.Key5, page.Deliver},
		{fyne.KeyModifierControl, fyne.Key1, page.Media},
		{fyne.KeyModifierShift, fyne.Key2, page.Edit},
	}
	for _, c := range cases {
		typeShortcut(t, sh, &desktop.CustomShortcut{KeyName: c.key, Modifier: c.mod})
		if sh.active.current != c.want {
			t.Fatalf("%v+%s opened %s, want %s", c.mod, c.key, sh.active.current, c.want)
		}
	}
	// From the manager a page shortcut returns to that page of the active project.
	sh.ShowManager()
	typeShortcut(t, sh, &desktop.CustomShortcut{KeyName: fyne.Key3, Modifier: fyne.KeyModifierAlt})
	if sh.showManager || sh.active.current != page.Color {
		t.Errorf("page shortcut in the manager: manager %v page %s", sh.showManager, sh.active.current)
	}

	// Shift+digit as typed key: the driver reports Shift with the digit itself.
	held := fyne.KeyModifier(0)
	sh.modifiers = func() fyne.KeyModifier { return held }
	held = fyne.KeyModifierShift
	sh.typedKey(&fyne.KeyEvent{Name: fyne.Key4})
	if sh.active.current != page.Audio {
		t.Fatalf("Shift+4 opened %s", sh.active.current)
	}
	// Focus lost while Shift was down: no key-up ever arrives. The next digit event
	// reports no modifier, so the digit goes to the page instead of switching.
	held = 0
	sh.typedKey(&fyne.KeyEvent{Name: fyne.Key1})
	if sh.active.current != page.Audio {
		t.Fatalf("a plain digit after focus loss switched to %s", sh.active.current)
	}
	if keys := made[page.Audio][0].keys; len(keys) != 1 || keys[0] != fyne.Key1 {
		t.Fatalf("plain digit not routed to the page: %v", keys)
	}
}

func TestHeldModifiersWithoutDesktopDriver(t *testing.T) {
	newTestShell(t)
	if m := heldModifiers(); m != 0 {
		t.Errorf("test driver reports modifiers %v", m)
	}
}

func TestSelectAllAndEscape(t *testing.T) {
	sh, _, made := newTestShell(t)
	sh.Start([]string{saveSample(t, "Auswahl", 1920, 1080, 25, 100)})
	edit(t, sh) // second clip
	s := sh.Active()
	typeShortcut(t, sh, &fyne.ShortcutSelectAll{})
	if len(s.Selected) != 2 {
		t.Fatalf("Ctrl+A selected %v", s.Selected)
	}
	s.SelectClip("")
	typeShortcut(t, sh, &desktop.CustomShortcut{KeyName: fyne.KeyA, Modifier: fyne.KeyModifierShortcutDefault})
	if len(s.Selected) != 2 {
		t.Fatalf("Ctrl+A (custom) selected %v", s.Selected)
	}

	// Esc goes to the page first; a page that handles it keeps the selection.
	ed := made[page.Edit][0]
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if n := len(ed.keys); n == 0 || ed.keys[n-1] != fyne.KeyEscape || len(s.Selected) != 2 {
		t.Fatalf("Esc not routed to the page: %v, selection %v", ed.keys, s.Selected)
	}
	// Unhandled, Esc clears the selection.
	ed.handle = false
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if len(s.Selected) != 0 || s.SelectedClip != "" {
		t.Fatalf("unhandled Esc kept %v", s.Selected)
	}

	// Ctrl+A in the manager does not touch the hidden project.
	sh.ShowManager()
	typeShortcut(t, sh, &fyne.ShortcutSelectAll{})
	if len(s.Selected) != 0 {
		t.Error("Ctrl+A in the manager must not select clips")
	}
}

func TestHelpListsFinalKeyMap(t *testing.T) {
	all := map[string]string{}
	for _, col := range shortcutColumns {
		for _, g := range col {
			for _, k := range g.Keys {
				all[k.Keys] += k.Action + ";"
			}
		}
	}
	want := map[string]string{
		"J": "Rückwärts", "K": "Stopp", "L": "8×", "R": "Ripple", "N": "Roll",
		"A": "Auswahlwerkzeug", "B": "Klinge", "Strg+A": "Alle Clips", "Esc": "Auswahl aufheben",
		"Strg+Mausrad": "Zoom", "Strg+Klick": "Auswahl", "Umschalt+Klick": "Auswahl",
		"Ziehen": "Timeline", "Umschalt+3": "Farbe",
	}
	for k, w := range want {
		if !strings.Contains(all[k], w) {
			t.Errorf("help %q = %q, want it to mention %q", k, all[k], w)
		}
	}
	if n := len(keyCaps("Strg+Umschalt+Z | Strg+Y")); n != 5+1+3 {
		t.Errorf("caps and separators = %d", n)
	}
	sh, w, _ := newTestShell(t)
	sh.showShortcuts()
	if w.Canvas().Overlays().Top() == nil {
		t.Fatal("help dialog not shown")
	}
}

func TestManagerSortAndSearch(t *testing.T) {
	sh, _, _ := newTestShell(t)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	for i, name := range []string{"Bravo", "Alpha", "Charlie Interview", "Delta Interview"} {
		p := saveSample(t, name, 1920, 1080, 25, 10)
		mt := base.Add(time.Duration(i) * time.Hour) // Delta newest
		_ = os.Chtimes(p, mt, mt)
	}
	sh.Start(nil)
	m := sh.mgr
	names := func() string {
		var n []string
		for _, c := range m.cards {
			n = append(n, c.d.Name)
		}
		return strings.Join(n, ",")
	}
	if got := names(); got != "Delta Interview,Charlie Interview,Alpha,Bravo" {
		t.Fatalf("default order %s", got)
	}
	m.sortSel.SetSelected("Name")
	if got := names(); got != "Alpha,Bravo,Charlie Interview,Delta Interview" {
		t.Fatalf("by name %s", got)
	}
	m.search.SetText("interview")
	if got := names(); got != "Charlie Interview,Delta Interview" || m.count.Text != "2 von 4" {
		t.Fatalf("search: %s (%s)", got, m.count.Text)
	}
	if m.card(m.selected) == nil {
		t.Error("selection should move to a visible card")
	}
	m.search.SetText("xyz")
	if len(m.cards) != 0 || !m.noMatchBox.Visible() || m.empty.Visible() {
		t.Fatal("no-match state not shown")
	}
	if !m.actions[0].Disabled() {
		t.Error("actions must be disabled without a visible project")
	}
	m.search.SetText("")
	sh.ShowManager() // reload keeps sort and search
	if got := names(); got != "Alpha,Bravo,Charlie Interview,Delta Interview" {
		t.Fatalf("after reload %s", got)
	}
}

func TestManagerThumbnailsRefreshWhenProjectChanges(t *testing.T) {
	sh, _, _ := newTestShell(t)
	q := kit.UseUIQueue(t)
	sh.thumbnails = true
	path := saveSample(t, "Vorschau", 320, 180, 25, 50)
	sh.Start(nil)
	m := sh.mgr
	t.Cleanup(m.cancelThumbnails)
	if !q.Until(func() bool { return m.cards[0].thumb.Image != nil }, 10*time.Second) {
		t.Fatal("thumbnail not rendered")
	}
	first := m.thumbs[path]
	if jobs := m.thumbJobs(); len(jobs) != 0 {
		t.Fatalf("cached thumbnail rendered again: %d jobs", len(jobs))
	}
	// Reloading without a change reuses the cache.
	sh.ShowManager()
	if m.cards[0].thumb.Image != first.img || len(m.thumbJobs()) != 0 {
		t.Fatal("unchanged project should keep its cached thumbnail")
	}
	// The project file changed (e.g. media relinked and saved): render again.
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(path, later, later)
	sh.ShowManager()
	if !q.Until(func() bool { return m.thumbs[path].key != first.key }, 10*time.Second) {
		t.Fatal("changed project file did not refresh its thumbnail")
	}
	// An open project with unsaved edits renders from the session.
	if err := sh.OpenFile(path); err != nil {
		t.Fatal(err)
	}
	key := m.thumbs[path].key
	edit(t, sh)
	sh.ShowManager()
	if !q.Until(func() bool { return m.thumbs[path].key != key }, 10*time.Second) {
		t.Fatal("edited open project did not refresh its thumbnail")
	}
	q.Settle(20*time.Millisecond, time.Second)
}

// orderPage records Close and Wait into a shared log.
type orderPage struct {
	fakePage
	log *[]string
}

func (o *orderPage) Close() { *o.log = append(*o.log, "close "+o.id) }
func (o *orderPage) Wait(time.Duration) bool {
	*o.log = append(*o.log, "wait "+o.id)
	return true
}

func TestQuitStopsPagesBeforeAudio(t *testing.T) {
	sh, _, _ := newTestShell(t)
	var log []string
	for i := range sh.pageDefs {
		id := sh.pageDefs[i].ID
		sh.pageDefs[i].New = func(*page.Context) page.Page {
			return &orderPage{fakePage: fakePage{id: id}, log: &log}
		}
	}
	sh.drivePlayback = func(s *session.Session) func() {
		return func() { log = append(log, "audio "+s.Project.Name) }
	}
	sh.Start([]string{saveSample(t, "Eins", 1920, 1080, 25, 10), saveSample(t, "Zwei", 1920, 1080, 25, 10)})
	sh.showPageID(page.Deliver)
	closed := false
	sh.closeWindow = func() { closed = true; log = append(log, "window") }
	sh.Quit()
	firstAudio := slices.IndexFunc(log, func(s string) bool { return strings.HasPrefix(s, "audio") })
	lastPage := -1
	for i, s := range log {
		if strings.HasPrefix(s, "close") || strings.HasPrefix(s, "wait") {
			lastPage = i
		}
	}
	if !closed || firstAudio < 0 || lastPage > firstAudio || log[len(log)-1] != "window" {
		t.Fatalf("shutdown order: %v", log)
	}
	if n := len(slices.DeleteFunc(slices.Clone(log), func(s string) bool { return !strings.HasPrefix(s, "audio") })); n != 2 {
		t.Fatalf("audio engines stopped %d times: %v", n, log)
	}
	sh.releaseAll() // Run calls it again after the loop: nothing left to stop
	if strings.Count(strings.Join(log, ";"), "audio") != 2 {
		t.Fatalf("second release stopped audio again: %v", log)
	}
}

// toastPage is a deliver page stand-in that keeps the shell's toast.
type toastPage struct {
	fakePage
	show func(title, message string, failed bool)
}

func (p *toastPage) SetToast(show func(string, string, bool)) { p.show = show }

func TestDeliverPageGetsWindowToast(t *testing.T) {
	sh, _, _ := newTestShell(t)
	tp := &toastPage{fakePage: fakePage{id: page.Deliver}}
	for i := range sh.pageDefs {
		if sh.pageDefs[i].ID == page.Deliver {
			sh.pageDefs[i].New = func(*page.Context) page.Page { return tp }
		}
	}
	sh.Start([]string{saveSample(t, "Toast", 1920, 1080, 25, 10)})
	sh.showPageID(page.Deliver)
	if tp.show == nil {
		t.Fatal("deliver page did not get the toast")
	}
	sh.showPageID(page.Edit) // the toast shows on every page
	tp.show("Export fertig", "„Film.mp4“ wurde gerendert.", false)
	card := sh.toast.card
	if !card.Visible() || card.title.Text != "Export fertig" || sh.toast.timer == nil {
		t.Fatal("toast not shown")
	}
	if !slices.Contains(sh.root.Objects, fyne.CanvasObject(sh.toast.layer)) {
		t.Fatal("toast layer missing from the window content")
	}
	test.Tap(card)
	if card.Visible() || sh.toast.timer != nil {
		t.Error("tapping the toast should hide it and stop its timer")
	}
	tp.show("Export fehlgeschlagen", "x", true)
	sh.Quit()
	if sh.toast.timer != nil {
		t.Error("quitting must stop the toast timer")
	}
}
