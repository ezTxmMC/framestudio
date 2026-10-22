package viewer

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/viewer/tip"
)

type transport struct {
	s          *session.Session
	tc         *canvas.Text
	dur        *canvas.Text
	inOut      *canvas.Text
	right      *fyne.Container
	play, loop *tip.Button
	playing    bool
	looping    bool
	content    fyne.CanvasObject
}

// NewTransport builds the transport bar; Transport is the contract alias.
func NewTransport(s *session.Session) fyne.CanvasObject { return newTransport(s).content }

func newTransport(s *session.Session) *transport {
	t := &transport{s: s}
	tips := tip.NewLayer(false)
	btn := func(icon fyne.Resource, label string, tap func()) *tip.Button {
		return tips.Button(icon, label, tap)
	}
	seek := func(f func() int) func() { return func() { s.Seek(f()) } }
	t.play = btn(theme.MediaPlayIcon(), "Abspielen (Leertaste)", s.TogglePlaying)
	t.loop = btn(themed(iconLoop), "Schleife", func() { s.SetLoop(!s.Loop) })
	buttons := container.NewHBox(
		btn(themed(iconToStart), "Zum Anfang (Pos1)", seek(func() int { return 0 })),
		btn(themed(iconPrevEdit), "Vorheriger Schnitt (Pfeil hoch)", seek(s.PrevEdit)),
		btn(themed(iconStepBack), "Einzelbild zurück (Pfeil links)", seek(func() int { return s.Playhead - 1 })),
		t.play,
		btn(themed(iconStepFwd), "Einzelbild vor (Pfeil rechts)", seek(func() int { return s.Playhead + 1 })),
		btn(themed(iconNextEdit), "Nächster Schnitt (Pfeil runter)", seek(s.NextEdit)),
		btn(themed(iconToEnd), "Zum Ende (Ende)", seek(func() int { return s.Project.Duration() })),
		gap(10),
		t.loop,
	)

	t.tc = kit.MonoText("", studiotheme.Text, 17)
	t.dur = kit.MonoText("", studiotheme.Text, 12)
	t.inOut = kit.MonoText("", studiotheme.Muted, 10)
	t.inOut.Alignment = fyne.TextAlignTrailing
	durLabel := canvas.NewText("Laufzeit ", studiotheme.Muted)
	durLabel.TextSize = 12
	// duration on top, the In/Out range below it when set
	t.right = container.NewVBox(layout.NewSpacer(),
		container.NewHBox(layout.NewSpacer(), durLabel, t.dur), t.inOut, layout.NewSpacer())
	bar := container.New(layout.NewCustomPaddedLayout(2, 2, 8, 8), container.NewBorder(nil, nil,
		container.NewCenter(t.tc), t.right, container.NewCenter(buttons)))
	t.content = container.NewStack(bar, tips)
	t.update()
	s.Subscribe(func(session.Change) { t.update() })
	return t
}

func (t *transport) update() {
	s := t.s
	setText(t.tc, s.Project.Timecode(s.Playhead))
	setText(t.dur, s.Project.Timecode(s.Project.Duration()))
	io := ""
	if s.HasRange() {
		io = "In " + s.Project.Timecode(s.In) + "  Out " + s.Project.Timecode(s.Out)
	}
	setText(t.inOut, io)
	if hide := io == ""; hide != t.inOut.Hidden {
		t.inOut.Hidden = hide
		t.right.Refresh()
	}
	if t.playing != s.Playing {
		t.playing = s.Playing
		t.play.Icon, t.play.Tip = theme.MediaPlayIcon(), "Abspielen (Leertaste)"
		if s.Playing {
			t.play.Icon, t.play.Tip = theme.MediaPauseIcon(), "Pause (Leertaste)"
		}
		t.play.Refresh()
	}
	if t.looping != s.Loop {
		t.looping = s.Loop
		t.loop.Icon = themed(iconLoop)
		if s.Loop {
			t.loop.Icon = accent(iconLoop)
		}
		t.loop.Refresh()
	}
}

func setText(t *canvas.Text, s string) {
	if t.Text != s {
		t.Text = s
		t.Refresh()
	}
}

func gap(w float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, 1))
	return r
}
