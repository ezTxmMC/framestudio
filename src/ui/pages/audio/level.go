package audio

import (
	"fmt"
	"math"
	"strings"

	"framestudio-proto/src/model"
)

// Fader range in decibels. MinDB and below is silence.
const (
	MinDB = -60.0
	MaxDB = 12.0
)

// DBToLinear converts a fader position in dB to the linear gain stored in the model.
func DBToLinear(db float64) float64 {
	if db <= MinDB {
		return 0
	}
	if db > MaxDB {
		db = MaxDB
	}
	return math.Pow(10, db/20)
}

// LinearToDB converts a linear gain to a fader position, clamped to MinDB..MaxDB.
func LinearToDB(v float64) float64 {
	if v <= 0 {
		return MinDB
	}
	return math.Max(MinDB, math.Min(MaxDB, 20*math.Log10(v)))
}

// FormatDB formats a fader position for readouts, e.g. "-3,5 dB", "+2,0 dB" or "-∞ dB".
func FormatDB(db float64) string {
	if db <= MinDB {
		return "-∞ dB"
	}
	db = math.Round(db*10) / 10
	s := fmt.Sprintf("%.1f dB", db)
	if db > 0 {
		s = "+" + s
	}
	if s == "-0.0 dB" {
		s = "0.0 dB"
	}
	return strings.Replace(s, ".", ",", 1)
}

// formatSeconds shows a frame count as seconds with German decimal comma, e.g. "1,20 s".
func formatSeconds(frames int, fps model.Rate) string {
	return strings.Replace(fmt.Sprintf("%.2f s", fps.FramesToSeconds(frames)), ".", ",", 1)
}
