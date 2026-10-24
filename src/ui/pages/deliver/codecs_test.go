package deliver

import (
	"framestudio-proto/src/export"
	"testing"
)

func TestCodecAndEncoderOptions(t *testing.T) {
	h := newHarness(t)
	f := h.pg.form
	for _, format := range []export.Format{export.FormatHEVC, export.FormatAV1} {
		f.format.SetSelectedIndex(formatIndex(format))
		f.encoder.SetSelectedIndex(2)
		o, err := f.options()
		if err != nil {
			t.Fatal(err)
		}
		if o.Format != format || o.Encoder != "vaapi" || !o.IncludeAudio || export.Extension(o.Format) != ".mp4" {
			t.Fatalf("options %+v", o)
		}
	}
	f.format.SetSelectedIndex(formatIndex(export.FormatGIF))
	if !f.encoder.Disabled() {
		t.Fatal("GIF offers unused encoder settings")
	}
}
