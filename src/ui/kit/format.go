package kit

import (
	"errors"
	"strconv"
	"strings"
)

// ParseFrames reads a duration typed by the user: timecode "HH:MM:SS:FF", "MM:SS:FF",
// plain seconds ("2", "1,5") or a frame count with an "f" suffix ("48f").
func ParseFrames(s string, fps int) (int, error) {
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
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) < 2 || len(parts) > 4 {
			return 0, errors.New("Timecode als HH:MM:SS:FF angeben")
		}
		for len(parts) < 4 {
			parts = append([]string{"0"}, parts...)
		}
		var n [4]int
		for i, p := range parts {
			v, err := strconv.Atoi(p)
			if err != nil || v < 0 {
				return 0, errors.New("Timecode als HH:MM:SS:FF angeben")
			}
			n[i] = v
		}
		if n[3] >= fps {
			return 0, errors.New("Frame-Anteil ist größer als die Bildrate")
		}
		return ((n[0]*60+n[1])*60+n[2])*fps + n[3], nil
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	if err != nil || f < 0 {
		return 0, errors.New("Sekunden, Timecode oder Frames (48f) angeben")
	}
	return int(f*float64(fps) + 0.5), nil
}
