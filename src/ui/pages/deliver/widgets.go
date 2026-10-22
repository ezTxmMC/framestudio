package deliver

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// fixedWidth lays out its children at full size and reports a fixed minimum width.
type fixedWidth struct{ w float32 }

func (f fixedWidth) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (f fixedWidth) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objs {
		if o.Visible() {
			h = max(h, o.MinSize().Height)
		}
	}
	return fyne.NewSize(f.w, h)
}

func withWidth(w float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(fixedWidth{w}, o)
}

func hline() fyne.CanvasObject {
	r := canvas.NewRectangle(studiotheme.Border)
	r.SetMinSize(fyne.NewSize(1, 1))
	return r
}

func smallText(s string, c color.Color, size float32) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = size
	return t
}

// vcenter centres o vertically without stretching it horizontally.
func vcenter(o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(layout.NewSpacer(), o, layout.NewSpacer())
}

const labelWidth = 76

// formRow is a settings line: a muted label column and the control.
func formRow(label string, control fyne.CanvasObject) fyne.CanvasObject {
	l := withWidth(labelWidth, vcenter(smallText(label, studiotheme.Muted, 12)))
	return container.NewBorder(nil, nil, l, nil, control)
}

// summaryRow is a read-only key/value line of the output summary.
func summaryRow(label string, value *canvas.Text) fyne.CanvasObject {
	l := withWidth(labelWidth, smallText(label, studiotheme.Muted, 12))
	return container.NewBorder(nil, nil, l, nil, value)
}

// setVisible shows or hides o and re-lays out its parent container.
func setVisible(o fyne.CanvasObject, on bool, parent fyne.CanvasObject) {
	if o.Visible() == on {
		return
	}
	if on {
		o.Show()
	} else {
		o.Hide()
	}
	if parent != nil {
		parent.Refresh()
	}
}

// ellipsize shortens s to n runes, keeping the end (the file name) visible.
func ellipsize(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n+1:])
}

// section is a group heading inside a panel, like Resolve's "Video" / "Audio" tabs.
func section(title string) fyne.CanvasObject {
	t := smallText(title, studiotheme.Faint, 11)
	t.TextStyle = fyne.TextStyle{Bold: true}
	return container.NewVBox(container.New(layout.NewCustomPaddedLayout(8, 2, 0, 0), t))
}

// presetIcon picks the file-type icon of a format.
func presetIcon(f export.Format) fyne.Resource {
	switch f {
	case export.FormatGIF, export.FormatPNG:
		return theme.MediaPhotoIcon()
	case export.FormatWAV:
		return theme.MediaMusicIcon()
	}
	return theme.MediaVideoIcon()
}

// presetSpec is the short technical line of a preset, e.g. "1920×1080 · H.264".
func presetSpec(o export.Options) string {
	switch o.Format {
	case export.FormatWAV:
		return "48 kHz · PCM"
	case export.FormatMP4:
		return fmt.Sprintf("%d×%d · H.264", o.Width, o.Height)
	case export.FormatWebM:
		return fmt.Sprintf("%d×%d · VP9", o.Width, o.Height)
	case export.FormatGIF:
		return fmt.Sprintf("%d×%d · 15 fps", o.Width, o.Height)
	}
	return fmt.Sprintf("%d×%d · PNG", o.Width, o.Height)
}

// presetRow is one selectable entry of the preset list.
type presetRow struct {
	widget.BaseWidget
	bg, bar  *canvas.Rectangle
	icon     *widget.Icon
	name     *canvas.Text
	spec     *canvas.Text
	selected bool
	hovered  bool
	onTap    func()
}

func newPresetRow(p export.Preset, tap func()) *presetRow {
	r := &presetRow{
		bg:    canvas.NewRectangle(color.Transparent),
		bar:   canvas.NewRectangle(color.Transparent),
		icon:  widget.NewIcon(presetIcon(p.Options.Format)),
		name:  smallText(p.Name, studiotheme.Text, 12.5),
		spec:  kit.MonoText(presetSpec(p.Options), studiotheme.Faint, 10.5),
		onTap: tap,
	}
	r.bg.CornerRadius = 3
	r.bar.SetMinSize(fyne.NewSize(3, 0))
	r.ExtendBaseWidget(r)
	return r
}

func (r *presetRow) SetSelected(on bool) {
	r.selected = on
	r.update()
}

func (r *presetRow) update() {
	switch {
	case r.selected:
		r.bg.FillColor = studiotheme.Raised
		r.bar.FillColor = studiotheme.Steel
		r.spec.Color = studiotheme.Muted
	case r.hovered:
		r.bg.FillColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0x0A}
		r.bar.FillColor = color.Transparent
		r.spec.Color = studiotheme.Faint
	default:
		r.bg.FillColor = color.Transparent
		r.bar.FillColor = color.Transparent
		r.spec.Color = studiotheme.Faint
	}
	r.bg.Refresh()
	r.bar.Refresh()
	r.spec.Refresh()
}

func (r *presetRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}

func (r *presetRow) MouseIn(*desktop.MouseEvent)    { r.hovered = true; r.update() }
func (r *presetRow) MouseMoved(*desktop.MouseEvent) {}
func (r *presetRow) MouseOut()                      { r.hovered = false; r.update() }

func (r *presetRow) CreateRenderer() fyne.WidgetRenderer {
	iconBox := container.New(fixedWidth{18}, r.icon)
	left := container.NewHBox(r.bar, iconBox)
	body := container.NewBorder(nil, nil, left, vcenter(r.spec), vcenter(r.name))
	padded := container.New(layout.NewCustomPaddedLayout(5, 5, 0, 8), body)
	return widget.NewSimpleRenderer(container.NewStack(r.bg, padded))
}

// segmented is a row of mutually exclusive buttons; the active one is steel.
type segmented struct {
	buttons []*widget.Button
	values  []string
	value   string
	content fyne.CanvasObject
	changed func(string)
}

func newSegmented(labels, values []string, changed func(string)) *segmented {
	s := &segmented{values: values, changed: changed}
	var objs []fyne.CanvasObject
	for i, l := range labels {
		v := values[i]
		b := widget.NewButton(l, func() { s.Set(v); s.changed(v) })
		s.buttons = append(s.buttons, b)
		objs = append(objs, b)
	}
	s.content = container.NewGridWithColumns(len(objs), objs...)
	return s
}

func (s *segmented) Set(v string) {
	s.value = v
	for i, b := range s.buttons {
		if s.values[i] == v {
			b.Importance = widget.HighImportance
		} else {
			b.Importance = widget.MediumImportance
		}
		b.Refresh()
	}
}

// frameCount is the number of timeline frames an export covers.
func frameCount(p *model.Project, o export.Options) int {
	from, to := export.Range(p, o)
	return max(to-from, 0)
}

// outputRate is the frame rate of the written file.
func outputRate(o export.Options, fps int) int {
	if o.Format == export.FormatGIF {
		return min(fps, 15)
	}
	return fps
}

// jobSummary is the compact technical line of a queue card.
func jobSummary(o export.Options, fps, frames int) string {
	parts := []string{strings.ToUpper(string(o.Format))}
	if o.Format != export.FormatWAV {
		parts = append(parts, fmt.Sprintf("%d×%d", o.Width, o.Height), fmt.Sprintf("%d fps", outputRate(o, fps)))
	}
	return strings.Join(append(parts, model.Timecode(frames, fps)), " · ")
}

// videoSummary / audioSummary describe the encoded streams of an export.
func videoSummary(o export.Options, fps int) string {
	codec := map[export.Format]string{export.FormatMP4: "H.264", export.FormatWebM: "VP9",
		export.FormatGIF: "GIF", export.FormatPNG: "PNG"}[o.Format]
	if codec == "" {
		return "–"
	}
	return fmt.Sprintf("%s · %d × %d · %d fps", codec, o.Width, o.Height, outputRate(o, fps))
}

func audioSummary(o export.Options) string {
	if !o.IncludeAudio {
		return "–"
	}
	switch o.Format {
	case export.FormatMP4:
		return "AAC 192 kb/s · 48 kHz Stereo"
	case export.FormatWebM:
		return "Opus 160 kb/s · 48 kHz Stereo"
	case export.FormatWAV:
		return "PCM 16 Bit · 48 kHz Stereo"
	}
	return "–"
}

// shortPath abbreviates the home directory as "~".
func shortPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return path
}

// formatBytes prints a file size the German way, e.g. "12,4 MB".
func formatBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, suffix := float64(n), []string{"kB", "MB", "GB", "TB"}
	i := -1
	for v >= unit && i < len(suffix)-1 {
		v /= unit
		i++
	}
	return strings.Replace(fmt.Sprintf("%.1f %s", v, suffix[i]), ".", ",", 1)
}

// formatClock prints a duration as M:SS or H:MM:SS.
func formatClock(d time.Duration) string {
	s := int((d + time.Second/2) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
