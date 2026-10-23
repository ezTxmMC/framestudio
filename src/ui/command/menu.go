package command

// Item is an entry of a menu: a command, a submenu, a separator or a dynamic list.
type Item struct {
	Command   string // command ID
	Label     string // overrides the command label
	Submenu   *Menu
	Separator bool
	Dynamic   string // ID of a generator registered with Registry.SetDynamic

	// Inline entries (from dynamic generators) run directly without a command ID.
	Run     func()
	Checked int // 0 = no check mark, 1 = unchecked toggle, 2 = checked
	Enabled func() bool
}

type Menu struct {
	Title string
	Items []Item
}

func Cmd(id string) Item { return Item{Command: id} }

// CmdL is a command with a shorter label for its menu, e.g. "Seitenweise" in the
// submenu "Timeline folgt dem Abspielkopf"; palette, keymap and help keep the full one.
func CmdL(id, label string) Item { return Item{Command: id, Label: label} }
func Sep() Item                  { return Item{Separator: true} }
func Sub(title string, items ...Item) Item {
	return Item{Label: title, Submenu: &Menu{Title: title, Items: items}}
}
func Dyn(id string) Item { return Item{Dynamic: id} }

// Dynamic submenu IDs. OWNER in brackets.
const (
	DynRecent     = "recent"     // [shell] recently opened projects
	DynProjects   = "projects"   // [shell] open project tabs, checked = active
	DynLayouts    = "layouts"    // [workspace] saved layouts, checked = current
	DynMonitors   = "monitors"   // [workspace] per monitor: program / clean feed fullscreen toggles
	DynWindows    = "windows"    // [workspace] open detached windows (focus/close)
	DynPanelPlace = "panelPlace" // [workspace] "Programm auf Monitor ▸" style entries
)

// MenuBar is the menu structure of the custom menu bar.
var MenuBar = []Menu{
	{Title: "Datei", Items: []Item{
		Cmd("file.new"), Cmd("file.open"), Sub("Zuletzt geöffnet", Dyn(DynRecent)), Cmd("file.manager"), Sep(),
		Cmd("file.save"), Cmd("file.saveAs"), Cmd("file.saveAll"), Cmd("file.revert"), Sep(),
		Cmd("media.import"), Cmd("media.importFolder"), Sep(),
		Cmd("deliver.export"), Cmd("deliver.addToQueue"), Sep(),
		Cmd("file.projectSettings"), Sep(),
		Cmd("file.close"), Cmd("file.closeAll"), Cmd("file.quit"),
	}},
	{Title: "Bearbeiten", Items: []Item{
		Cmd("edit.undo"), Cmd("edit.redo"), Sep(),
		Cmd("edit.cut"), Cmd("edit.copy"), Cmd("edit.paste"), Cmd("edit.duplicate"), Sep(),
		Cmd("edit.delete"), Cmd("edit.rippleDelete"), Sep(),
		Cmd("edit.selectAll"), Cmd("edit.selectForward"), Cmd("edit.selectTrack"), Cmd("edit.selectLinked"), Cmd("edit.deselect"), Sep(),
		Cmd("app.keymap"), Cmd("app.settings"),
	}},
	{Title: "Clip", Items: []Item{
		Cmd("clip.split"), Cmd("clip.splitAll"), Cmd("clip.rename"), Sep(),
		Sub("Clipfarbe", Cmd("clip.color.0"), Cmd("clip.color.1"), Cmd("clip.color.2"), Cmd("clip.color.3"),
			Cmd("clip.color.4"), Cmd("clip.color.5"), Cmd("clip.color.6")),
		Sub("Blenden", Cmd("clip.fadeIn"), Cmd("clip.fadeOut"), Cmd("clip.removeFades")),
		Sub("Anpassen", Cmd("clip.fit.contain"), Cmd("clip.fit.cover"), Cmd("clip.fit.stretch"), Sep(), Cmd("clip.resetOpacity")),
		Sub("Farbkorrektur", Cmd("grade.copy"), Cmd("grade.paste"), Cmd("grade.applySelection"), Cmd("grade.applyAll"), Sep(), Cmd("grade.reset")),
		Sep(), Cmd("clip.addTitle"), Cmd("clip.addLowerThird"), Cmd("clip.addColor"), Sep(),
		Cmd("clip.showInPool"), Cmd("media.reveal"),
	}},
	{Title: "Timeline", Items: []Item{
		Sub("Werkzeug", Cmd("tool.select"), Cmd("tool.blade"), Cmd("tool.ripple"), Cmd("tool.roll")),
		Cmd("timeline.snapping"), Cmd("timeline.bladeAll"), Sep(),
		Cmd("timeline.addVideoTrack"), Cmd("timeline.addAudioTrack"), Cmd("timeline.renameTrack"), Cmd("timeline.deleteTrack"), Cmd("timeline.removeEmptyTracks"), Sep(),
		Cmd("timeline.setIn"), Cmd("timeline.setOut"), Cmd("timeline.rangeFromSelection"), Cmd("timeline.clearRange"), Sep(),
		Sub("Marker", Cmd("marker.add"), Cmd("marker.edit"), Cmd("marker.delete"), Sep(), Cmd("marker.next"), Cmd("marker.prev"), Sep(), Cmd("marker.deleteAll")),
		Sep(), Cmd("timeline.zoomIn"), Cmd("timeline.zoomOut"), Cmd("timeline.zoomFit"),
		Sub("Timeline folgt dem Abspielkopf", CmdL("timeline.follow.page", "Seitenweise"), CmdL("timeline.follow.smooth", "Fließend"), CmdL("timeline.follow.off", "Nicht folgen")),
		Sep(), Cmd("timeline.showFilmstrip"), Cmd("timeline.showWaveforms"),
	}},
	{Title: "Wiedergabe", Items: []Item{
		Cmd("play.toggle"), Cmd("play.inOut"), Cmd("play.reverse"), Cmd("play.stop"), Cmd("play.forward"), Sep(),
		Cmd("play.start"), Cmd("play.end"), Cmd("play.gotoIn"), Cmd("play.gotoOut"), Sep(),
		Cmd("play.frameBack"), Cmd("play.frameForward"), Cmd("play.secondBack"), Cmd("play.secondForward"),
		Cmd("play.prevEdit"), Cmd("play.nextEdit"), Sep(),
		Cmd("play.loop"), Cmd("play.scrubAudio"), Cmd("play.mute"),
		Sub("Vorschauqualität", CmdL("play.quality.auto", "Automatisch"), CmdL("play.quality.full", "Volle Auflösung"),
			CmdL("play.quality.half", "Halbe Auflösung"), CmdL("play.quality.quarter", "Viertel-Auflösung")),
	}},
	{Title: "Ansicht", Items: []Item{
		Sub("Seite", Cmd("view.page.media"), Cmd("view.page.edit"), Cmd("view.page.color"), Cmd("view.page.audio"), Cmd("view.page.deliver")),
		Sep(), Cmd("view.fullscreen"), Cmd("view.viewerFullscreen"), Sep(),
		Cmd("view.safeArea"), Cmd("grade.compare"),
		Sub("Scopes", Cmd("scopes.histogram"), Cmd("scopes.waveform"), Cmd("scopes.vectorscope")),
		Sub("Bereiche der Schnittseite", Cmd("edit.hide.viewer"), Cmd("edit.hide.library"), Cmd("edit.hide.inspector")),
		Sep(), Cmd("view.pageBar"), Cmd("view.statusBar"), Sep(), Cmd("app.palette"),
	}},
	{Title: "Arbeitsbereich", Items: []Item{
		Cmd("workspace.monitors"), Sub("Monitore", Dyn(DynMonitors)), Sep(),
		Sub("Fenster lösen", Cmd("workspace.detach.program"), Cmd("workspace.detach.cleanfeed"), Sep(),
			Cmd("workspace.detach.timeline"), Cmd("workspace.detach.inspector"), Cmd("workspace.detach.library"),
			Cmd("workspace.detach.mediapool"), Cmd("workspace.detach.scopes"), Cmd("workspace.detach.mixer"), Cmd("workspace.detach.queue")),
		Sub("Gelöste Fenster", Dyn(DynWindows)), Cmd("workspace.closeDetached"), Sep(),
		Sub("Layouts", Dyn(DynLayouts)), Cmd("workspace.saveLayout"), Cmd("workspace.manageLayouts"), Cmd("workspace.resetLayout"), Sep(),
		Cmd("workspace.saveOnQuit"),
	}},
	{Title: "Fenster", Items: []Item{
		Dyn(DynProjects), Sep(), Cmd("window.nextProject"), Cmd("window.prevProject"),
	}},
	{Title: "Hilfe", Items: []Item{
		Cmd("help.shortcuts"), Cmd("app.palette"), Sep(), Cmd("help.diagnostics"), Cmd("help.configFolder"), Sep(), Cmd("help.about"),
	}},
}
