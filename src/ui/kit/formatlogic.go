package kit

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"framestudio-proto/src/model"
)

// Format is a project or export format: resolution, frame rate, drop-frame display.
type Format struct {
	Width, Height int
	FPS           model.Rate
	DropFrame     bool
}

// ResPreset is one entry of the resolution chooser.
type ResPreset struct {
	Group string
	Name  string
	W, H  int
}

// Label is the select text, "Full HD  1920 × 1080".
func (p ResPreset) Label() string { return fmt.Sprintf("%s  %d × %d", p.Name, p.W, p.H) }

// ResPresets are the resolutions every picker offers, grouped as in the chooser.
var ResPresets = []ResPreset{
	{"Video", "HD 720p", 1280, 720},
	{"Video", "Full HD 1080p", 1920, 1080},
	{"Video", "UHD 4K", 3840, 2160},
	{"Video", "UHD 8K", 7680, 4320},
	{"Kino", "DCI 2K", 2048, 1080},
	{"Kino", "DCI 4K", 4096, 2160},
	{"SD", "PAL", 720, 576},
	{"SD", "NTSC", 720, 480},
	{"Social", "Hochkant 9:16", 1080, 1920},
	{"Social", "Quadrat 1:1", 1080, 1080},
	{"Social", "Hochformat 4:5", 1080, 1350},
	{"Ultrawide", "Ultrawide 21:9", 2560, 1080},
	{"Ultrawide", "Ultrawide WQHD", 3440, 1440},
}

// ResCustom is the chooser entry for free width and height.
const ResCustom = "Eigene…"

// RatePresetLabels are the select entries of the frame rate chooser.
func RatePresetLabels() []string {
	out := make([]string, len(model.RatePresets))
	for i, r := range model.RatePresets {
		out[i] = r.Label()
	}
	return out
}

// RateCustom is the chooser entry for a typed frame rate.
const RateCustom = "Eigene…"

// Ratio is an aspect ratio preset.
type Ratio struct {
	Name string
	W, H float64
}

// Ratios are the aspect ratio presets of the lock.
var Ratios = []Ratio{
	{"16:9", 16, 9}, {"9:16", 9, 16}, {"1:1", 1, 1}, {"4:5", 4, 5}, {"21:9", 21, 9}, {"4:3", 4, 3}, {"2,39:1", 2.39, 1},
}

// ParseDimension reads a width or height ("Breite", "Höhe") typed by the user.
func ParseDimension(text, name string) (int, error) {
	t := strings.TrimSpace(text)
	rng := fmt.Sprintf("ganze Zahl von %d bis %d", model.MinSize, model.MaxSize)
	if t == "" {
		return 0, fmt.Errorf("Die %s fehlt. Bitte eine %s eingeben.", name, rng)
	}
	n, err := strconv.Atoi(t)
	if err != nil {
		if strings.Trim(t, "0123456789") == "" { // too many digits for an int
			return 0, fmt.Errorf("Die %s ist zu groß. Bitte eine %s eingeben.", name, rng)
		}
		return 0, fmt.Errorf("„%s“ ist keine gültige %s. Bitte eine %s eingeben.", t, name, rng)
	}
	if n < model.MinSize || n > model.MaxSize {
		return 0, fmt.Errorf("Die %s %d liegt außerhalb des Bereichs. Bitte eine %s eingeben.", name, n, rng)
	}
	return n, nil
}

// OtherSide returns the other side of a locked aspect ratio: the height for a width
// (fromWidth) or the width for a height. The result is rounded and clamped to the limits.
func OtherSide(v int, ratio float64, fromWidth bool) int {
	if ratio <= 0 || v <= 0 {
		return v
	}
	var o float64
	if fromWidth {
		o = float64(v) / ratio
	} else {
		o = float64(v) * ratio
	}
	return min(max(int(math.Round(o)), model.MinSize), model.MaxSize)
}

// AspectText describes the aspect of a size: "16:9" for common ratios, else "1,43:1".
func AspectText(w, h int) string {
	if w <= 0 || h <= 0 {
		return "–"
	}
	g := gcd(w, h)
	if a, b := w/g, h/g; a <= 32 && b <= 32 {
		return fmt.Sprintf("%d:%d", a, b)
	}
	r := float64(w) / float64(h)
	for _, p := range Ratios {
		if math.Abs(r-p.W/p.H) < 0.004*p.W/p.H {
			return p.Name
		}
	}
	return strings.Replace(strconv.FormatFloat(r, 'f', 2, 64), ".", ",", 1) + ":1"
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Megapixels is "2,1 MP".
func Megapixels(w, h int) string {
	return strings.Replace(strconv.FormatFloat(float64(w)*float64(h)/1e6, 'f', 1, 64), ".", ",", 1) + " MP"
}

// SizeReadout is the line under the size entries: "16:9 · 2,1 MP".
func SizeReadout(w, h int) string { return AspectText(w, h) + " · " + Megapixels(w, h) }

// ParseRateText reads a typed frame rate. note is "" or a hint: that the input was snapped
// to an NTSC rate, or rounded to 1/1000 precision.
func ParseRateText(text string) (r model.Rate, note string, err error) {
	t := strings.TrimSpace(text)
	if t == "" {
		return model.Rate{}, "", errors.New("Die Bildrate fehlt. Bitte eine Zahl wie 25 oder 29,97 oder einen Bruch wie 30000/1001 eingeben.")
	}
	r, err = model.ParseRate(t)
	if err != nil {
		return model.Rate{}, "", err
	}
	if !strings.Contains(t, "/") {
		f, _ := strconv.ParseFloat(strings.Replace(strings.TrimSpace(strings.TrimSuffix(strings.ToLower(t), "fps")), ",", ".", 1), 64)
		switch {
		case r.IsNTSC() && math.Abs(f-r.Float()) > 1e-9:
			note = fmt.Sprintf("Wird als %s (%s) verwendet, die genaue NTSC-Bildrate.", r.Fraction(), r.Label())
		case r.Den != 1 && math.Abs(f-r.Float()) > 1e-9:
			note = "Auf 1/1000 genau gerundet: " + r.Fraction() + "."
		}
	}
	return r, note, nil
}

// RateDetail is the exact rational of a rate for the hint line, "30000/1001 = 29,97 fps".
func RateDetail(r model.Rate) string {
	if r.Reduced().Den == 1 {
		return r.Label()
	}
	return r.Fraction() + " = " + r.Label()
}
