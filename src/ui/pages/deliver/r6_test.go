package deliver

import (
	"image/png"
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
)

func TestExportRateOverridePlumbing(t *testing.T) {
	h := newHarness(t)
	f := h.pg.form
	h.configure(t, export.FormatMP4)
	o, err := f.options()
	must(t, err)
	if !o.FPS.IsZero() {
		t.Fatalf("default follows the project rate, got %v", o.FPS)
	}
	if f.rateNote.Visible() {
		t.Error("no conversion note without an override")
	}

	f.rate.Select().SetSelected(model.NTSC30.Label())
	o, err = f.options()
	must(t, err)
	if !o.FPS.Equal(model.NTSC30) {
		t.Fatalf("override not passed on: %v", o.FPS)
	}
	if !f.rateNote.Visible() || !strings.Contains(f.rateNote.Text, "umgerechnet") {
		t.Errorf("conversion note %q", f.rateNote.Text)
	}
	if !strings.Contains(f.sumVideo.Text, "29,97 fps") || f.sumFrames.Text != "90 Bilder bei 29,97 fps" || !f.sumFramesRow.Visible() {
		t.Errorf("summary %q / %q", f.sumVideo.Text, f.sumTime.Text)
	}
	if f.presetAt != -1 {
		t.Error("a rate override no longer matches the preset")
	}

	// same rate as the project is no override
	f.rate.Select().SetSelected(model.Rate25.Label())
	if o, _ = f.options(); !o.FPS.IsZero() {
		t.Errorf("25 fps equals the project: %v", o.FPS)
	}

	// a typed fraction
	f.rate.Select().SetSelected(kit.RateCustom)
	f.rate.CustomEntry().SetText("24000/1001")
	if o, err = f.options(); err != nil || !o.FPS.Equal(model.NTSC24) {
		t.Fatalf("custom fraction: %v %v", o.FPS, err)
	}
	f.rate.CustomEntry().SetText("500")
	if _, err = f.options(); err == nil || !strings.Contains(err.Error(), "Bildrate") {
		t.Errorf("invalid rate must be an error: %v", err)
	}

	// WAV and PNG ignore the rate
	f.rate.CustomEntry().SetText("24")
	f.format.SetSelectedIndex(formatIndex(export.FormatWAV))
	if o, _ = f.options(); !o.FPS.IsZero() {
		t.Error("WAV has no frame rate")
	}
}

func TestExportEvenNoteAndScale(t *testing.T) {
	h := newHarness(t)
	f := h.pg.form
	h.configure(t, export.FormatMP4)
	f.width.SetText("161")
	if !f.evenNote.Visible() || !strings.Contains(f.evenNote.Text, "160") {
		t.Errorf("even note %q", f.evenNote.Text)
	}
	f.format.SetSelectedIndex(formatIndex(export.FormatGIF))
	if f.evenNote.Visible() {
		t.Error("GIF needs no even size")
	}
	f.format.SetSelectedIndex(formatIndex(export.FormatMP4))
	f.res.SetSelectedIndex(1) // 50 %
	o, err := f.options()
	must(t, err)
	if o.Width != 960 || o.Height != 540 {
		t.Errorf("50 %% scale gave %dx%d", o.Width, o.Height)
	}
	f.res.SetSelectedIndex(2) // 25 %
	if o, _ = f.options(); o.Width != 480 || o.Height != 270 {
		t.Errorf("25 %% scale gave %dx%d", o.Width, o.Height)
	}
	f.res.SetSelected(resCustom)
	if f.width.Text != "480" || f.height.Text != "270" {
		t.Errorf("custom starts from the previous choice: %sx%s", f.width.Text, f.height.Text)
	}
	f.width.SetText("17x")
	if _, err = f.options(); err == nil {
		t.Error("17x must be rejected")
	}
}

func TestExportShots(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT")
	}
	h := newHarness(t)
	f := h.pg.form
	f.rate.Select().SetSelected(model.NTSC30.Label())
	f.res.SetSelected(resCustom)
	f.width.SetText("1279")
	t.Logf("form min width %.0f, scroll %.0f", f.formBox.MinSize().Width, f.scroll.Size().Width)
	f.scroll.ScrollToBottom()
	for path, size := range map[string]fyne.Size{"/tmp/fs-r6-export-1440.png": {Width: 1440, Height: 900 - shellChrome},
		"/tmp/fs-r6-export-1280.png": {Width: 1280, Height: 800 - shellChrome}} {
		h.w.Resize(size)
		file, err := os.Create(path)
		must(t, err)
		must(t, png.Encode(file, h.w.Canvas().Capture()))
		file.Close()
	}
}
