package kit

import (
	"errors"
	"strconv"
	"strings"

	"framestudio-proto/src/model"
)

// ParseFrames reads a duration typed by the user: timecode "HH:MM:SS:FF", "MM:SS:FF" (":"
// and ";" both separate the frames), plain seconds ("2", "1,5") or a frame count with an "f"
// suffix ("48f"). Timecodes are counted in the given mode (drop-frame skips labels, see
// model.ParseTimecode); the result is a real frame count at rate.
func ParseFrames(s string, rate model.Rate, drop bool) (int, error) {
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
	if err != nil || f < 0 {
		return 0, errors.New("Sekunden, Timecode oder Frames (48f) angeben")
	}
	return rate.SecondsToFrames(f), nil
}
