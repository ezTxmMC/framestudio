package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
	"time"
)

// Limits of project settings.
const (
	MinFPS  = 1
	MaxFPS  = 240
	MinSize = 16
	MaxSize = 16384
)

// Rate is a rational frame rate Num/Den in frames per second (24000/1001 = 23.976 fps).
// The zero Rate is "unset"; every method treats it as 25 fps so broken data cannot divide
// by zero. Rates stored in projects are always reduced (see Reduced).
//
// JSON: an integer rate is a plain number (25), so old project files that stored "fps":25
// load unchanged. Any other rate is {"num":24000,"den":1001}. A JSON number with a
// fraction (29.97) is accepted too and snapped with SnapRate.
//
// Rounding rules of the exact integer maths (all use 128-bit intermediates, no floats):
//   - FramesToDuration / FrameStart round the time UP to the next nanosecond, so
//     FrameAt(FrameStart(n)) == n for every n.
//   - DurationToFrames / FrameAt round DOWN (the frame that is showing at that time).
//   - SamplesAt rounds to the nearest sample (half up); every position is computed from the
//     frame number directly, so errors never accumulate.
type Rate struct {
	Num int `json:"num"`
	Den int `json:"den"`
}

// Common rates.
var (
	Rate24  = Rate{24, 1}
	Rate25  = Rate{25, 1}
	Rate30  = Rate{30, 1}
	Rate48  = Rate{48, 1}
	Rate50  = Rate{50, 1}
	Rate60  = Rate{60, 1}
	Rate120 = Rate{120, 1}
	NTSC24  = Rate{24000, 1001} // 23.976
	NTSC30  = Rate{30000, 1001} // 29.97
	NTSC60  = Rate{60000, 1001} // 59.94
	NTSC120 = Rate{120000, 1001}
)

// RatePresets are the frame rates offered in dialogs.
var RatePresets = []Rate{NTSC24, Rate24, Rate25, NTSC30, Rate30, Rate48, Rate50, NTSC60, Rate60, Rate120}

// IntRate returns the integer rate n/1.
func IntRate(n int) Rate { return Rate{n, 1} }

func (r Rate) norm() Rate {
	if r.Num <= 0 || r.Den <= 0 {
		return Rate25
	}
	return r
}

// IsZero reports whether the rate is unset.
func (r Rate) IsZero() bool { return r.Num == 0 && r.Den == 0 }

// Valid reports whether the rate lies in MinFPS..MaxFPS.
func (r Rate) Valid() bool {
	return r.Num > 0 && r.Den > 0 && r.Num <= maxTerm && r.Den <= maxTerm &&
		r.Num >= MinFPS*r.Den && int64(r.Num) <= int64(MaxFPS)*int64(r.Den)
}

// maxTerm bounds numerator and denominator so that Den*1e9 and Num*sampleRate stay far
// inside int64.
const maxTerm = 1 << 31

// SnapFraction snaps an exact fraction to the integer or NTSC rate it lies within 0.0005 fps
// of (2997/100 becomes 30000/1001); any other fraction is returned reduced and unchanged.
func SnapFraction(r Rate) Rate {
	r = r.Reduced()
	if r.Num <= 0 || r.Den <= 0 {
		return r
	}
	if s := SnapRate(r.Float()); s.Den == 1 || s.Den == 1001 {
		return s
	}
	return r
}

// Or returns r, or def if r is unset or invalid.
func (r Rate) Or(def Rate) Rate {
	if r.Num <= 0 || r.Den <= 0 {
		return def
	}
	return r
}

// Reduced returns the rate in lowest terms (an unset rate stays unset).
func (r Rate) Reduced() Rate {
	if r.Num <= 0 || r.Den <= 0 {
		return r
	}
	g := gcd(r.Num, r.Den)
	return Rate{r.Num / g, r.Den / g}
}

// Equal reports whether both rates are the same value (24000/1001 equals 48000/2002).
func (r Rate) Equal(o Rate) bool {
	return int64(r.Num)*int64(o.Den) == int64(o.Num)*int64(r.Den)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Float is the rate as frames per second (23.976...).
func (r Rate) Float() float64 {
	r = r.norm()
	return float64(r.Num) / float64(r.Den)
}

// Nominal is the whole-number timecode base: 24 for 23.976, 30 for 29.97, 60 for 59.94,
// else the rounded rate. It is the number of frames counted in one timecode second and the
// size of a one-second step in the editor.
func (r Rate) Nominal() int {
	r = r.norm()
	if r.IsNTSC() {
		return (r.Num + r.Den - 1) / r.Den
	}
	return max(1, int(math.Round(r.Float())))
}

// IsNTSC reports whether the rate is n*1000/1001.
func (r Rate) IsNTSC() bool {
	r = r.Reduced()
	return r.Den == 1001 && r.Num > 0
}

// CanDropFrame reports whether SMPTE drop-frame timecode exists for the rate (29.97, 59.94).
func (r Rate) CanDropFrame() bool {
	r = r.Reduced()
	return r == NTSC30 || r == NTSC60
}

// String is the rate as decimal with at most three decimals, "." separated: "25", "23.976",
// "29.97".
func (r Rate) String() string {
	s := strconv.FormatFloat(r.Float(), 'f', 3, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// Label is the German display text, "23,976 fps".
func (r Rate) Label() string { return strings.Replace(r.String(), ".", ",", 1) + " fps" }

// Fraction is the exact "num/den" text for ffmpeg ("30000/1001", "25").
func (r Rate) Fraction() string {
	r = r.norm().Reduced()
	if r.Den == 1 {
		return strconv.Itoa(r.Num)
	}
	return fmt.Sprintf("%d/%d", r.Num, r.Den)
}

// SecondsString is the time of frame n as decimal seconds with nanosecond precision
// ("0.041708334"), the form ffmpeg's -ss/-t options understand (they take no fractions).
func (r Rate) SecondsString(n int) string { return DurationSeconds(r.FramesToDuration(n)) }

// DurationSeconds formats d as decimal seconds with nine decimals, using integer maths only.
func DurationSeconds(d time.Duration) string {
	sign := ""
	if d < 0 {
		sign, d = "-", -d
	}
	return fmt.Sprintf("%s%d.%09d", sign, d/time.Second, d%time.Second)
}

// MarshalJSON writes integer rates as a number, others as {"num","den"}.
func (r Rate) MarshalJSON() ([]byte, error) {
	r = r.Reduced()
	if r.Den == 1 || r.Den == 0 {
		return []byte(strconv.Itoa(r.Num)), nil
	}
	return []byte(fmt.Sprintf(`{"num":%d,"den":%d}`, r.Num, r.Den)), nil
}

// UnmarshalJSON reads a number (integer or decimal, snapped), an object or a "a/b" string.
func (r *Rate) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || string(b) == "null":
		*r = Rate{}
	case b[0] == '{':
		type plain Rate
		var a plain
		if err := json.Unmarshal(b, &a); err != nil {
			return err
		}
		v := Rate(a).Reduced()
		if v.Num > maxTerm || v.Den > maxTerm {
			return errors.New("Bildrate zu groß: Zähler und Nenner dürfen höchstens 2147483648 sein")
		}
		*r = v
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		v, err := ParseRate(s)
		if err != nil {
			return err
		}
		*r = v
	default:
		if n, err := strconv.Atoi(string(b)); err == nil {
			*r = Rate{n, 1}.Reduced()
			if n <= 0 {
				*r = Rate{}
			}
			return nil
		}
		f, err := strconv.ParseFloat(string(b), 64)
		if err != nil {
			return err
		}
		if f <= 0 {
			*r = Rate{}
			return nil
		}
		*r = SnapRate(f)
	}
	return nil
}

// SnapRate turns a decimal frame rate into a Rate. A value within 0.0005 of an NTSC rate
// (n*1000/1001) becomes that rational; one within 0.0005 of a whole number becomes the
// integer rate; every other value becomes the rational with denominator 1000 (reduced), i.e.
// 1/1000 precision.
func SnapRate(f float64) Rate {
	if f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return Rate{}
	}
	if n := math.Round(f); math.Abs(f-n) <= 0.0005 && n >= 1 {
		return Rate{int(n), 1}
	}
	for n := 1; n <= 2*MaxFPS; n++ {
		if math.Abs(f-float64(n)*1000/1001) <= 0.0005 {
			return Rate{n * 1000, 1001}.Reduced()
		}
	}
	return Rate{int(math.Round(f * 1000)), 1000}.Reduced()
}

// ParseRate reads user input: "25", "23.976", "29,97", "24000/1001", optionally followed by
// "fps". The result is snapped (see SnapRate) and must lie in 1..240 fps.
func ParseRate(s string) (Rate, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	t = strings.TrimSpace(strings.TrimSuffix(t, "fps"))
	bad := fmt.Errorf("%q ist keine gültige Bildrate. Eine Zahl wie 25 oder 29,97 oder einen Bruch wie 30000/1001 eingeben", s)
	if t == "" {
		return Rate{}, bad
	}
	var r Rate
	if a, b, ok := strings.Cut(t, "/"); ok {
		n, err1 := strconv.Atoi(strings.TrimSpace(a))
		d, err2 := strconv.Atoi(strings.TrimSpace(b))
		if err1 != nil || err2 != nil || n <= 0 || d <= 0 {
			return Rate{}, bad
		}
		r = SnapFraction(Rate{n, d})
	} else {
		f, err := strconv.ParseFloat(strings.Replace(t, ",", ".", 1), 64)
		if err != nil || f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
			return Rate{}, bad
		}
		r = SnapRate(f)
	}
	if !r.Valid() {
		return Rate{}, fmt.Errorf("Die Bildrate muss zwischen %d und %d fps liegen", MinFPS, MaxFPS)
	}
	return r, nil
}

// ---- exact conversions ----

const (
	modeFloor = iota
	modeCeil
	modeRound
)

// mulDiv returns a*b/c for non-negative a, b and positive c using a 128-bit intermediate. A
// result that does not fit saturates at MaxInt64.
func mulDiv(a, b, c uint64, mode int) int64 {
	hi, lo := bits.Mul64(a, b)
	if hi >= c {
		return math.MaxInt64
	}
	q, rem := bits.Div64(hi, lo, c)
	switch mode {
	case modeCeil:
		if rem > 0 {
			q++
		}
	case modeRound:
		if rem >= c-rem {
			q++
		}
	}
	if q > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(q)
}

// scale returns x*num/den with the given rounding, for any sign of x.
func scale(x int64, num, den int64, mode int) int64 {
	if x >= 0 {
		return mulDiv(uint64(x), uint64(num), uint64(den), mode)
	}
	m := mode
	switch mode {
	case modeFloor:
		m = modeCeil
	case modeCeil:
		m = modeFloor
	}
	return -mulDiv(uint64(-x), uint64(num), uint64(den), m)
}

// FramesToDuration is the time at which frame f starts (and the length of f frames),
// rounded up to whole nanoseconds.
func (r Rate) FramesToDuration(f int) time.Duration {
	r = r.norm()
	return time.Duration(scale(int64(f), int64(r.Den)*int64(time.Second), int64(r.Num), modeCeil))
}

// FrameStart is the time of the start of frame n; same as FramesToDuration.
func (r Rate) FrameStart(n int) time.Duration { return r.FramesToDuration(n) }

// DurationToFrames is the number of whole frames in d (rounded down).
func (r Rate) DurationToFrames(d time.Duration) int {
	r = r.norm()
	return int(scale(int64(d), int64(r.Num), int64(r.Den)*int64(time.Second), modeFloor))
}

// FrameAt is the frame that is showing at time t; same as DurationToFrames.
func (r Rate) FrameAt(t time.Duration) int { return r.DurationToFrames(t) }

// DurationToFramesNearest is DurationToFrames rounded to the nearest frame.
func (r Rate) DurationToFramesNearest(d time.Duration) int {
	r = r.norm()
	return int(scale(int64(d), int64(r.Num), int64(r.Den)*int64(time.Second), modeRound))
}

// FramesToSeconds is f frames in seconds (float, for display and rough maths).
func (r Rate) FramesToSeconds(f int) float64 {
	r = r.norm()
	return float64(f) * float64(r.Den) / float64(r.Num)
}

// SecondsToFrames is the nearest whole frame count for sec seconds.
func (r Rate) SecondsToFrames(sec float64) int {
	v := math.Round(sec * r.Float())
	switch {
	case math.IsNaN(v):
		return 0
	case v >= math.MaxInt32*1024:
		return math.MaxInt32 * 1024
	case v <= -math.MaxInt32*1024:
		return -math.MaxInt32 * 1024
	}
	return int(v)
}

// SamplesAt is the audio sample position of frame f at sampleRate, rounded to the nearest
// sample: f*sampleRate*Den/Num. At 23.976 and 48 kHz every frame is exactly 2002 samples.
func (r Rate) SamplesAt(f, sampleRate int) int64 {
	r = r.norm()
	return scale(int64(f), int64(sampleRate)*int64(r.Den), int64(r.Num), modeRound)
}

// FrameAtSample is the frame containing sample position s: the last frame f with
// SamplesAt(f) <= s.
func (r Rate) FrameAtSample(s int64, sampleRate int) int {
	r = r.norm()
	f := int(scale(s, int64(r.Num), int64(r.Den)*int64(sampleRate), modeFloor))
	for f > 0 && r.SamplesAt(f, sampleRate) > s {
		f--
	}
	for r.SamplesAt(f+1, sampleRate) <= s {
		f++
	}
	return f
}

// ConvertFrames maps frame position f at rate from to the nearest frame at rate to
// (the same moment in time).
func ConvertFrames(f int, from, to Rate) int {
	from, to = from.norm(), to.norm()
	return int(scale(int64(f), int64(to.Num)*int64(from.Den), int64(to.Den)*int64(from.Num), modeRound))
}

// ---- timecode ----

// Timecode formats frame f as "HH:MM:SS:FF" (non-drop-frame). The frame part counts to the
// nominal rate (24 at 23.976). With drop and a rate that allows it (29.97, 59.94) it is SMPTE
// drop-frame "HH:MM:SS;FF": frame numbers 0 and 1 (0..3 at 59.94) are skipped in every
// minute except each tenth, so the clock matches real time. f is always the real frame
// count; only the display changes. Negative frames show as 0.
func Timecode(f int, r Rate, drop bool) string {
	if f < 0 {
		f = 0
	}
	base := r.Nominal()
	sep := ":"
	if drop && r.CanDropFrame() {
		sep = ";"
		f = dropAdjust(f, base)
	}
	s := f / base
	return fmt.Sprintf("%02d:%02d:%02d%s%02d", s/3600, s/60%60, s%60, sep, f%base)
}

// dropAdjust converts a real frame count to the frame count of a virtual NDF counter whose
// digits are the drop-frame timecode.
func dropAdjust(f, base int) int {
	drop := base / 15 // 2 at 30, 4 at 60
	per10 := base*600 - 9*drop
	perMin := base*60 - drop
	d, m := f/per10, f%per10
	f += 9 * drop * d
	if m >= drop {
		f += drop * ((m - drop) / perMin)
	}
	return f
}

// ParseTimecode is the inverse of Timecode: it reads "HH:MM:SS:FF" or "HH:MM:SS;FF"
// (separators : ; . accepted; the semicolon is not required for drop-frame, drop says how
// to count). It returns the real frame number. The frame part must be below the nominal
// rate, and in drop-frame the skipped labels (e.g. 01:00:00;00) are rejected.
func ParseTimecode(s string, r Rate, drop bool) (int, error) {
	bad := fmt.Errorf("%q ist kein gültiger Timecode. Das Format ist HH:MM:SS:FF", s)
	t := strings.NewReplacer(";", ":", ".", ":").Replace(strings.TrimSpace(s))
	parts := strings.Split(t, ":")
	if len(parts) != 4 {
		return 0, bad
	}
	var n [4]int
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || v < 0 {
			return 0, bad
		}
		n[i] = v
	}
	base := r.Nominal()
	if n[3] >= base {
		return 0, fmt.Errorf("Die Frames im Timecode müssen kleiner als %d sein", base)
	}
	if n[1] >= 60 || n[2] >= 60 {
		return 0, bad
	}
	f := ((n[0]*60+n[1])*60+n[2])*base + n[3]
	if drop && r.CanDropFrame() {
		dr := base / 15
		if n[2] == 0 && n[3] < dr && n[1]%10 != 0 {
			return 0, fmt.Errorf("Der Timecode %s existiert im Drop-Frame-Zählen nicht. Frame %d oder höher eingeben", s, dr)
		}
		total := n[0]*60 + n[1]
		f -= dr * (total - total/10)
	}
	return f, nil
}

// ---- settings ----

// ValidateSettings checks resolution and frame rate ranges.
func ValidateSettings(s Settings) error {
	if !s.FPS.Valid() {
		return fmt.Errorf("Die Bildrate muss zwischen %d und %d fps liegen", MinFPS, MaxFPS)
	}
	if s.Width < MinSize || s.Height < MinSize || s.Width > MaxSize || s.Height > MaxSize {
		return fmt.Errorf("Die Auflösung muss in Breite und Höhe zwischen %d und %d Pixeln liegen", MinSize, MaxSize)
	}
	return nil
}

// EvenSize rounds both sizes down to even numbers, as video encoders require.
func EvenSize(w, h int) (int, int) { return w &^ 1, h &^ 1 }

var errNoMedia = errors.New("Kein Video und kein Bild im Medienpool. Zuerst Medien importieren")

// MatchFirstMedia returns the settings that match the first video or image in the media pool
// (its size, and its frame rate if it is a video with a valid rate). Other settings stay.
func (p *Project) MatchFirstMedia() (Settings, error) {
	st := p.Settings
	for _, m := range p.Media {
		if (m.Kind != KindVideo && m.Kind != KindImage) || m.Width <= 0 || m.Height <= 0 {
			continue
		}
		st.Width, st.Height = m.Width, m.Height
		if m.Kind == KindVideo && m.FPS.Valid() {
			st.FPS = m.FPS
		}
		return st, ValidateSettings(st)
	}
	return st, errNoMedia
}

// LabelFrame maps real frame f to its position in timecode-label space: the frame count a
// non-drop-frame counter would need to show the same digits. Without drop-frame it is f.
// Rulers use it to place ticks on whole timecode seconds and minutes.
func LabelFrame(f int, r Rate, drop bool) int {
	if f < 0 {
		f = 0
	}
	if drop && r.CanDropFrame() {
		return dropAdjust(f, r.Nominal())
	}
	return f
}

// FrameFromLabel is the inverse of LabelFrame. ok is false for label positions that do
// not exist in drop-frame (the labels skipped at the start of most minutes).
func FrameFromLabel(v int, r Rate, drop bool) (f int, ok bool) {
	if v < 0 {
		return 0, false
	}
	if !drop || !r.CanDropFrame() {
		return v, true
	}
	base := r.Nominal()
	dr := base / 15
	per10 := base*600 - 9*dr
	perMin := base*60 - dr
	d, m := v/(base*600), v%(base*600)
	f = d * per10
	if m < base*60 {
		return f + m, true
	}
	j := m / (base * 60) // minute within the block, 1..9
	rest := m - j*base*60
	if rest < dr {
		return 0, false
	}
	return f + base*60 + (j-1)*perMin + rest - dr, true
}
