package kit

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"framestudio-proto/src/model"
)

// ParseFrames reads a duration typed by the user: timecode "HH:MM:SS:FF", "MM:SS:FF" (":"
// and ";" both separate the frames), plain seconds ("2", "1,5") or a frame count with an "f"
// suffix ("48f"). Timecodes are counted in the given mode (drop-frame skips labels, see
// model.ParseTimecode); the result is a real frame count at rate.
func ParseFrames(s string, rate model.Rate, drop bool) (int, error) {
	n, err := parseFrames(s, rate, drop)
	if err == nil && n > maxParsedFrames(rate) {
		return 0, errors.New("Wert zu groß: höchstens 24 Stunden eingeben")
	}
	return n, err
}

func maxParsedFrames(rate model.Rate) int { return rate.SecondsToFrames(24 * 3600) }

func parseFrames(s string, rate model.Rate, drop bool) (int, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, errors.New("Wert fehlt")
	}
	if strings.HasSuffix(s, "f") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "f"))
		if err != nil || n < 0 {
			return 0, errors.New("Frames als ganze Zahl angeben, z.B. 48f")
		}
		return n, nil
	}
	if strings.ContainsAny(s, ":;") {
		parts := strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == ';' })
		if len(parts) < 2 || len(parts) > 4 {
			return 0, errors.New("Timecode als HH:MM:SS:FF angeben")
		}
		for len(parts) < 4 {
			parts = append([]string{"0"}, parts...)
		}
		return model.ParseTimecode(strings.Join(parts, ":"), rate, drop)
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) || f > 24*3600 {
		return 0, errors.New("Sekunden, Timecode oder Frames (48f) angeben")
	}
	return rate.SecondsToFrames(f), nil
}
