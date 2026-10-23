package viewer

import (
	"fmt"
	"image/color"
	"math"

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

const (
	tipPlay  = "Abspielen (Leertaste, L)"
	tipPause = "Pause (Leertaste, K)"
)

type transport struct {
	s          *session.Session
	tc         *canvas.Text
	rate       *canvas.Text // shuttle speed, e.g. "2×" or "−1×"; empty at normal play
	rateChip   *canvas.Rectangle
	meter      *Meter
	dur        *canvas.Text
	inOut      *canvas.Text
	right      *fyne.Container
	play, loop *tip.Button
	playing    bool
	looping    bool
	content    fyne.CanvasObject
	unsub      func()
}

// NewTransport builds the transport bar; Transport is the contract alias.
func NewTransport(s *session.Session) fyne.CanvasObject { return newTransport(s, false).content }

// TransportBar is a transport bar with an optional master level meter. Unlike
// Transport it can be closed, which stops the meter and unsubscribes.
type TransportBar struct{ t *transport }

// NewTransportBar builds a transport bar; with meter set it shows a stereo master
// meter right of the duration that polls liveaudio.Levels while playing.
func NewTransportBar(s *session.Session, meter bool) *TransportBar {
	return &TransportBar{t: newTransport(s, meter)}
}

// Content is the bar's canvas object.
func (b *TransportBar) Content() fyne.CanvasObject { return b.t.content }

// Meter returns the level meter, or nil without one.
func (b *TransportBar) Meter() *Meter { return b.t.meter }

// Rate returns the shown shuttle rate text ("" at normal speed).
func (b *TransportBar) Rate() string { return b.t.rate.Text }

// Close stops the meter and unsubscribes from the session.
func (b *TransportBar) Close() {
	b.t.unsub()
	if b.t.meter != nil {
		b.t.meter.Close()
	}
}

func newTransport(s *session.Session, meter bool) *transport {
	t := &transport{s: s}
	tips := tip.NewLayer(false)
	btn := func(icon fyne.Resource, label string, tap func()) *tip.Button {
		return tips.Button(icon, label, tap)
	}
	seek := func(f func() int) func() { return func() { s.Seek(f()) } }
	t.play = btn(theme.MediaPlayIcon(), tipPlay, s.TogglePlaying)
	t.loop = btn(themed(iconLoop), "Schleife", func() { s.SetLoop(!s.Loop) })
	buttons := container.NewHBox(
		btn(themed(iconToStart), "Zum Anfang (Pos1)", seek(func() int { return 0 })),
		btn(themed(iconPrevEdit), "Vorheriger Schnitt (Pfeil hoch)", seek(s.PrevEdit)),
		btn(themed(iconStepBack), "Einzelbild zurück (Pfeil links)", seek(func() int { return s.Playhead - 1 })),
		t.play,
		btn(themed(iconStepFwd), "Einzelbild vor (Pfeil rechts)", seek(func() int { return s.Playhead + 1 })),
		btn(themed(iconNextEdit), "Nächster Schnitt (Pfeil runter)", seek(s.NextEdit)),
		btn(themed(iconToEnd), "Zum Ende (Ende)", seek(func() int { return s.Project.Duration() })),
		gap(6),
		t.loop,
	)

	t.tc = kit.MonoText("", studiotheme.Text, 16)
	t.rate = kit.MonoText("", studiotheme.Steel, 13)
	t.rate.TextStyle.Bold = true
	t.rate.Alignment = fyne.TextAlignCenter
	// a fixed slot so the bar does not shift when the rate appears
	t.rateChip = canvas.NewRectangle(color.Transparent)
	t.rateChip.StrokeColor = studiotheme.Steel
	t.rateChip.StrokeWidth = 1
	t.rateChip.CornerRadius = 3
	t.rateChip.Hide()
	slot := canvas.NewRectangle(color.Transparent)
	slot.SetMinSize(fyne.NewSize(34, 19))
	rateBox := container.NewHBox(gap(2), container.NewCenter(container.NewStack(slot, t.rateChip, t.rate)))
	t.dur = kit.MonoText("", studiotheme.Text, 12)
	t.inOut = kit.MonoText("", studiotheme.Muted, 10)
	t.inOut.Alignment = fyne.TextAlignTrailing
	durLabel := canvas.NewText("Laufzeit ", studiotheme.Muted)
	durLabel.TextSize = 12
	// duration on top, the length of the In/Out range below it when set
	t.right = container.NewVBox(layout.NewSpacer(),
		container.NewHBox(layout.NewSpacer(), durLabel, t.dur), t.inOut, layout.NewSpacer())
	right := fyne.CanvasObject(t.right)
	if meter {
		t.meter = NewMeter(s)
		right = container.NewHBox(t.right, gap(6), container.NewCenter(t.meter))
	}
	left := container.NewHBox(container.NewCenter(t.tc), rateBox)
	bar := container.New(layout.NewCustomPaddedLayout(2, 2, 6, 6), container.NewBorder(nil, nil,
		left, right, container.NewCenter(buttons)))
	t.content = container.NewStack(bar, tips)
	t.update()
	t.unsub = s.Subscribe(func(session.Change) { t.update() })
	return t
}

// RateLabel formats a shuttle rate for the transport: "" when stopped or at normal
// speed, otherwise e.g. "2×", "8×", "−1×", "−4×".
func RateLabel(playing bool, rate float64) string {
	if !playing || rate == 1 || rate == 0 {
		return ""
	}
	s := fmt.Sprintf("%g×", math.Abs(rate))
	if rate < 0 {
		s = "−" + s
	}
	return s
}

func (t *transport) update() {
	s := t.s
	setText(t.tc, s.Project.Timecode(s.Playhead))
	setText(t.dur, s.Project.Timecode(s.Project.Duration()))
	io := ""
	if s.HasRange() {
		io = "Bereich " + s.Project.Timecode(s.Out-s.In)
	}
	setText(t.inOut, io)
	if hide := io == ""; hide != t.inOut.Hidden {
		t.inOut.Hidden = hide
		t.right.Refresh()
	}
	if r := RateLabel(s.Playing, s.Rate); r != t.rate.Text || t.rate.Color != studiotheme.Steel {
		// the accent may have changed in the settings since the bar was built
		t.rate.Color, t.rateChip.StrokeColor = studiotheme.Steel, studiotheme.Steel
		t.rate.Text = r
		t.rate.Refresh()
		t.rateChip.Hidden = r == ""
		t.rateChip.Refresh()
	}
	if t.playing != s.Playing {
		t.playing = s.Playing
		t.play.Icon, t.play.Tip = theme.MediaPlayIcon(), tipPlay
		if s.Playing {
			t.play.Icon, t.play.Tip = theme.MediaPauseIcon(), tipPause
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
