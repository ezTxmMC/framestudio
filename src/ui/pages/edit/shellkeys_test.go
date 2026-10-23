package edit

import (
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
)

// bindTransport binds the playback commands globally, as the shell does.
func bindTransport(r *command.Registry, s *session.Session) {
	seek := func(f func() int) func() { return func() { s.Seek(f()) } }
	r.Bind(command.Global,
		command.Binding{ID: "play.toggle", Run: s.TogglePlaying},
		command.Binding{ID: "play.reverse", Run: func() { s.Shuttle(-1) }},
		command.Binding{ID: "play.stop", Run: func() { s.Shuttle(0) }},
		command.Binding{ID: "play.forward", Run: func() { s.Shuttle(+1) }},
		command.Binding{ID: "play.start", Run: seek(func() int { return 0 })},
		command.Binding{ID: "play.end", Run: seek(func() int { return s.Project.Duration() })},
		command.Binding{ID: "play.frameBack", Run: seek(func() int { return s.Playhead - 1 })},
		command.Binding{ID: "play.frameForward", Run: seek(func() int { return s.Playhead + 1 })},
		command.Binding{ID: "play.prevEdit", Run: seek(s.PrevEdit)},
		command.Binding{ID: "play.nextEdit", Run: seek(s.NextEdit)},
	)
}

// resetSettings starts a test with the factory settings (kept in memory: Load is never
// called in tests) and restores them afterwards.
func resetSettings(t interface{ Cleanup(func()) }) {
	defaults := func() { _ = settings.Update(func(s *settings.Settings) { *s = settings.Defaults() }) }
	defaults()
	t.Cleanup(defaults)
}
