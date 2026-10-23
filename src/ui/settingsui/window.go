package settingsui

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/winchrome"
)

// Category IDs.
const (
	catGeneral  = "general"
	catPlayback = "playback"
	catPerf     = "perf"
	catAudio    = "audio"
	catTimeline = "timeline"
	catMedia    = "media"
	catExport   = "export"
	catDisplay  = "display"
	catMonitors = "monitors"
	catKeymap   = "keymap"
)

// Window geometry.
const (
	winW, winH   = 980, 680
	sidebarW     = 212
	controlW     = 300
	rowPadX      = 18
	rowPadY      = 9
	searchW      = 280
	warningAlpha = 0xFF
)

// Warning is the colour of validation messages and the restart badge: amber, so the
// tally red stays reserved for the playhead.
var warning = color.NRGBA{0xE0, 0xA0, 0x48, warningAlpha}

type category struct {
	id, title, desc string
	icon            fyne.Resource
	fields          []*field
	// extra builds custom content shown below the fields (monitor overview); full
	// replaces the field list entirely (keymap editor).
	extra func() fyne.CanvasObject
	full  func() fyne.CanvasObject
	// resetExtra runs after the fields of the category were reset (keymap).
	resetExtra func()

	item      *catItem
	extraObj  fyne.CanvasObject
	fullObj   fyne.CanvasObject
	onShow    func() // refresh live information when the category is shown
	matchHits int
}

type window struct {
	app    fyne.App
	win    fyne.Window
	parent fyne.Window
	reg    *command.Registry

	cats   []*category
	fields []*field
	cur    *category
	query  string

	loading bool // widgets are being synced from the settings: ignore their callbacks
	closed  bool
	unsub   func()

	search  *widget.Entry
	body    *fyne.Container // right side content
	content *fyne.Container // banner + body + footer
	banner  *fyne.Container
	status  *canvas.Text
	sidebar *fyne.Container

	// confirm asks before destructive actions; tests replace it.
	confirm func(title, message, ok string, yes func())
	// pickFolder asks for a folder and calls done on the UI thread ("" = cancelled).
	pickFolder func(title, start string, done func(path string))
	// Custom title bar (Display.CustomChrome): see chrome.go.
	custom      bool
	undecorated bool
	chrome      *winchrome.Chrome
	sideObj     fyne.CanvasObject // category sidebar with its separator
	contentObj  fyne.CanvasObject // banner, category page and status line
	infoRefresh func()            // refreshes the title bar status text (Darstellung)

	// restartBtn is shown when a restart-only setting changed and Restart is set.
	restartBtn *widget.Button
	bannerText *canvas.Text
}

func newWindow(a fyne.App, parent fyne.Window, reg *command.Registry) *window {
	w := &window{app: a, parent: parent, reg: reg}
	w.win = a.NewWindow("Einstellungen – Frame Studio")
	w.win.Resize(fyne.NewSize(winW, winH))
	w.win.CenterOnScreen()
	w.win.SetPadded(false)
	w.confirm = w.dialogConfirm
	w.pickFolder = w.defaultPickFolder
	startupSettings()

	w.custom = winchrome.Wanted(settings.Get().Display)
	w.cats = w.buildCategories()
	w.layout()
	w.install()
	winchrome.OnClose(w.win, w.win.Close)
	w.load(settings.Get())
	lastChrome := settings.Get().Display
	w.unsub = settings.Subscribe(func(s settings.Settings) {
		if !w.closed {
			w.load(s)
			if s.Display.CustomChrome != lastChrome.CustomChrome || s.Display.WindowButtons != lastChrome.WindowButtons {
				w.applyChrome(lastChrome, s.Display)
			}
			lastChrome = s.Display
		}
	})
	w.win.SetOnClosed(w.closedByUser)
	w.win.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
		if e.Name == fyne.KeyEscape {
			if w.query != "" {
				w.search.SetText("")
				return
			}
			w.win.Close()
		}
	})
	w.win.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: fyne.KeyModifierShortcutDefault}, func(fyne.Shortcut) {
		w.win.Canvas().Focus(w.search)
	})
	return w
}

func (w *window) closedByUser() {
	w.closed = true
	if w.chrome != nil {
		w.chrome.Close()
		w.chrome = nil
	}
	winchrome.Forget(w.win)
	if w.unsub != nil {
		w.unsub()
		w.unsub = nil
	}
	if current == w {
		current = nil
	}
}

// layout builds the parts of the window that stay while the title bar mode changes: the
// search box, the category sidebar and the content. install puts them together.
func (w *window) layout() {
	w.search = widget.NewEntry()
	w.search.SetPlaceHolder("Einstellungen durchsuchen  (Strg+F)")
	w.search.ActionItem = widget.NewIcon(theme.SearchIcon())
	w.search.OnChanged = w.setQuery

	// Sidebar.
	items := make([]fyne.CanvasObject, 0, len(w.cats))
	for _, c := range w.cats {
		c := c
		c.item = newCatItem(c, func() { w.selectCategory(c) })
		items = append(items, c.item)
	}
	resetAll := widget.NewButtonWithIcon("Alles zurücksetzen…", theme.HistoryIcon(), w.askResetAll)
	resetAll.Importance = widget.LowImportance
	resetAll.Alignment = widget.ButtonAlignLeading
	w.sidebar = container.NewVBox(items...)
	sideBG := canvas.NewRectangle(studiotheme.Lane)
	side := container.NewStack(sideBG, container.New(layout.NewCustomPaddedLayout(8, 8, 6, 6),
		container.NewBorder(nil, resetAll, nil, nil, w.sidebar)))
	sideFixed := container.New(&fixedWidth{w: sidebarW}, side)

	// Restart banner (hidden until a restart-only setting changed).
	w.bannerText = canvas.NewText("", studiotheme.Text)
	w.bannerText.TextSize = 12
	w.restartBtn = widget.NewButtonWithIcon("Jetzt neu starten", theme.ViewRefreshIcon(), w.restartNow)
	w.restartBtn.Importance = widget.HighImportance
	bannerBG := canvas.NewRectangle(withAlpha(warning, 0x26))
	bannerBG.StrokeColor = withAlpha(warning, 0x80)
	bannerBG.StrokeWidth = 1
	bannerBG.CornerRadius = 4
	icon := widget.NewIcon(theme.InfoIcon())
	banner := container.NewStack(bannerBG, container.New(layout.NewCustomPaddedLayout(4, 4, 10, 6),
		container.NewBorder(nil, nil, icon, w.restartBtn, container.NewHBox(container.NewCenter(w.bannerText)))))
	w.banner = container.New(layout.NewCustomPaddedLayout(10, 0, rowPadX, rowPadX), banner)
	w.banner.Hide()

	// Status line.
	w.status = canvas.NewText(statusIdle, studiotheme.Muted)
	if err := settings.Blocked(); err != nil {
		w.status.Text = "Änderungen wirken, werden aber nicht gespeichert: " + err.Error()
		w.status.Color = warning
	}
	w.status.TextSize = 11

	w.body = container.NewStack()
	contentBG := canvas.NewRectangle(studiotheme.Panel)
	statusLine := canvas.NewRectangle(studiotheme.Border)
	statusLine.SetMinSize(fyne.NewSize(1, 1))
	footer := container.NewVBox(statusLine, container.New(layout.NewCustomPaddedLayout(5, 6, rowPadX, rowPadX), w.status))
	w.content = container.NewBorder(w.banner, footer, nil, nil, w.body)
	content := container.NewStack(contentBG, w.content)

	sideLine := canvas.NewRectangle(studiotheme.Border)
	sideLine.SetMinSize(fyne.NewSize(1, 1))
	w.sideObj = container.NewHBox(sideFixed, sideLine)
	w.contentObj = content
}

const statusIdle = "Änderungen wirken sofort und werden automatisch gespeichert."

// showCategory selects a category by ID or title; unknown or empty = Allgemein.
func (w *window) showCategory(id string) {
	id = strings.TrimSpace(strings.ToLower(id))
	if w.query != "" {
		w.search.SetText("")
	}
	for _, c := range w.cats {
		if c.id == id || strings.ToLower(c.title) == id {
			w.selectCategory(c)
			return
		}
	}
	if w.cur == nil {
		w.selectCategory(w.cats[0])
	}
}

func (w *window) selectCategory(c *category) {
	if w.query != "" {
		w.query = ""
		w.search.OnChanged = nil
		w.search.SetText("")
		w.search.OnChanged = w.setQuery
	}
	w.cur = c
	for _, o := range w.cats {
		o.item.setState(o == c, false, 0)
	}
	if c.onShow != nil {
		c.onShow()
	}
	w.body.Objects = []fyne.CanvasObject{w.categoryView(c)}
	w.body.Refresh()
}

// categoryView is the page of one category: header, then the rows (or the full view).
func (w *window) categoryView(c *category) fyne.CanvasObject {
	resetBtn := widget.NewButtonWithIcon("Auf Standard zurücksetzen", theme.HistoryIcon(), func() { w.askResetCategory(c) })
	resetBtn.Importance = widget.LowImportance
	head := w.header(c.title, c.desc, resetBtn)
	if c.full != nil {
		if c.fullObj == nil {
			c.fullObj = c.full()
		}
		return container.NewBorder(head, nil, nil, nil,
			container.New(layout.NewCustomPaddedLayout(0, 8, rowPadX, rowPadX), c.fullObj))
	}
	rows := make([]fyne.CanvasObject, 0, len(c.fields)+1)
	for _, f := range c.fields {
		rows = append(rows, f.row)
	}
	if c.extra != nil {
		if c.extraObj == nil {
			c.extraObj = c.extra()
		}
		rows = append(rows, c.extraObj)
	}
	scroll := container.NewVScroll(container.New(layout.NewCustomPaddedLayout(0, 14, 0, 0), container.NewVBox(rows...)))
	return container.NewBorder(head, nil, nil, nil, scroll)
}

// header is the title block of a page.
func (w *window) header(title, desc string, tool fyne.CanvasObject) fyne.CanvasObject {
	t := canvas.NewText(title, studiotheme.Text)
	t.TextSize = 19
	t.TextStyle = fyne.TextStyle{Bold: true}
	d := canvas.NewText(desc, studiotheme.Muted)
	d.TextSize = 12
	texts := container.New(&vstack{gap: 3}, t, d)
	var right fyne.CanvasObject = layout.NewSpacer()
	if tool != nil {
		right = container.NewCenter(tool)
	}
	line := canvas.NewRectangle(studiotheme.Border)
	line.SetMinSize(fyne.NewSize(1, 1))
	return container.NewVBox(
		container.New(layout.NewCustomPaddedLayout(16, 12, rowPadX, rowPadX-6), container.NewBorder(nil, nil, nil, right, texts)),
		line)
}

// setQuery filters all settings by label (and explanation) across categories.
func (w *window) setQuery(q string) {
	w.query = strings.TrimSpace(q)
	if w.query == "" {
		if w.cur == nil {
			w.cur = w.cats[0]
		}
		w.selectCategory(w.cur)
		return
	}
	needle := strings.ToLower(w.query)
	var objs []fyne.CanvasObject
	total := 0
	for _, c := range w.cats {
		hits := 0
		var rows []fyne.CanvasObject
		catMatch := strings.Contains(strings.ToLower(c.title), needle)
		for _, f := range c.fields {
			if catMatch || f.matches(needle) {
				rows = append(rows, f.row)
				hits++
			}
		}
		if c.id == catKeymap && catMatch {
			hits++
			open := widget.NewButtonWithIcon("Tastenkürzel öffnen", theme.NavigateNextIcon(), func() { w.selectCategory(c) })
			open.Importance = widget.LowImportance
			rows = append(rows, container.New(layout.NewCustomPaddedLayout(4, 4, rowPadX, rowPadX), container.NewHBox(open)))
		}
		c.item.setState(false, hits == 0, hits)
		if hits == 0 {
			continue
		}
		total += hits
		objs = append(objs, w.sectionTitle(c))
		objs = append(objs, rows...)
	}
	var view fyne.CanvasObject
	head := w.header("Suche", fmt.Sprintf("%s für „%s“", plural(total, "Treffer", "Treffer"), w.query), nil)
	if total == 0 {
		msg := canvas.NewText(fmt.Sprintf("Keine Einstellung gefunden für „%s“.", w.query), studiotheme.Muted)
		msg.TextSize = 13
		view = container.NewBorder(head, nil, nil, nil,
			container.New(layout.NewCustomPaddedLayout(24, 0, rowPadX, rowPadX), container.NewVBox(msg)))
	} else {
		view = container.NewBorder(head, nil, nil, nil,
			container.NewVScroll(container.New(layout.NewCustomPaddedLayout(0, 14, 0, 0), container.NewVBox(objs...))))
	}
	w.body.Objects = []fyne.CanvasObject{view}
	w.body.Refresh()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// sectionTitle is the category caption above search results.
func (w *window) sectionTitle(c *category) fyne.CanvasObject {
	t := canvas.NewText(strings.ToUpper(c.title), studiotheme.Muted)
	t.TextSize = 11
	t.TextStyle = fyne.TextStyle{Bold: true}
	return container.New(layout.NewCustomPaddedLayout(14, 2, rowPadX, rowPadX), t)
}

// set changes the settings from a control. Ignored while widgets are being synced.
func (w *window) set(fn func(s *settings.Settings)) {
	if w.loading || w.closed {
		return
	}
	if err := settings.Update(fn); err != nil {
		w.setStatus("Einstellungen konnten nicht gespeichert werden: "+err.Error(), true)
		return
	}
	w.setStatus(statusIdle, false)
}

func (w *window) setStatus(text string, bad bool) {
	w.status.Text = text
	w.status.Color = studiotheme.Muted
	if bad {
		w.status.Color = warning
	}
	w.status.Refresh()
}

// load syncs every control with s (after any change, also from elsewhere).
func (w *window) load(s settings.Settings) {
	w.loading = true
	defer func() { w.loading = false }()
	for _, f := range w.fields {
		f.load(s)
	}
	w.updateBanner(s)
	if w.infoRefresh != nil {
		w.infoRefresh()
	}
	for _, c := range w.cats {
		if c.item != nil {
			c.item.Refresh() // accent colour may have changed
		}
	}
}

// pendingRestart lists the restart-only settings that differ from the running program.
func pendingRestart(s settings.Settings) []string {
	st := startupSettings()
	var out []string
	if normPlatform(s.Display.Platform) != normPlatform(st.Display.Platform) && envOverride("Fenstersystem") == "" {
		out = append(out, "Fenstersystem")
	}
	if s.Display.Scale != st.Display.Scale && envOverride("Skalierung") == "" {
		out = append(out, "Skalierung")
	}
	if pendingChromeRestart(s) {
		out = append(out, "Eigene Titelleiste")
	}
	return out
}

func normPlatform(p string) string {
	if p == "" {
		return "auto"
	}
	return strings.ToLower(p)
}

func (w *window) updateBanner(s settings.Settings) {
	pending := pendingRestart(s)
	if len(pending) == 0 {
		if w.banner.Visible() {
			w.banner.Hide()
			w.content.Refresh()
		}
		return
	}
	what := "Die neue Einstellung für " + pending[0] + " wird"
	if len(pending) > 1 {
		what = "Die neuen Einstellungen für " + strings.Join(pending, " und ") + " werden"
	}
	if Restart != nil {
		w.bannerText.Text = what + " nach einem Neustart wirksam."
		w.restartBtn.Show()
	} else {
		w.bannerText.Text = what + " wirksam, wenn Frame Studio neu startet."
		w.restartBtn.Hide()
	}
	w.bannerText.Refresh()
	w.banner.Show()
	w.content.Refresh()
	w.content.Refresh()
}

func (w *window) restartNow() {
	if Restart == nil {
		return
	}
	w.win.Close()
	Restart()
}

// resetting the settings.

func (w *window) askResetCategory(c *category) {
	w.confirm("„"+c.title+"“ zurücksetzen?",
		"Alle Einstellungen dieser Kategorie erhalten wieder ihre Standardwerte.", "Zurücksetzen",
		func() { w.resetCategory(c) })
}

func (w *window) resetCategory(c *category) {
	d := settings.Defaults()
	w.set(func(s *settings.Settings) {
		for _, f := range c.fields {
			f.reset(s, d)
		}
	})
	if c.resetExtra != nil {
		c.resetExtra()
	}
}

func (w *window) askResetAll() {
	w.confirm("Alle Einstellungen zurücksetzen?",
		"Sämtliche Einstellungen und Tastenkürzel erhalten wieder ihre Standardwerte.", "Alles zurücksetzen",
		w.resetAll)
}

func (w *window) resetAll() {
	if err := settings.Reset(); err != nil {
		w.setStatus("Einstellungen konnten nicht gespeichert werden: "+err.Error(), true)
	}
	if w.reg != nil {
		w.reg.SetKeymap(map[string]string{})
	}
}

func (w *window) dialogConfirm(title, message, ok string, yes func()) {
	d := dialog.NewConfirm(title, message, func(b bool) {
		if b {
			yes()
		}
	}, w.win)
	d.SetConfirmText(ok)
	d.SetDismissText("Abbrechen")
	d.Show()
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}
