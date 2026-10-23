package settings

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"framestudio-proto/src/model"
)

// Template is a resolution and frame rate offered for new projects. Built-in templates
// have fixed IDs ("1080p25"); user templates ("Eigene Vorlage") live in General.Templates
// and have IDs "user:<n>".
type Template struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Width     int        `json:"width"`
	Height    int        `json:"height"`
	FPS       model.Rate `json:"fps"`
	DropFrame bool       `json:"dropFrame,omitempty"`
	User      bool       `json:"-"`
}

// Label is the one-line description, "1080p HD · 1920×1080 · 25 fps".
func (t Template) Label() string {
	return fmt.Sprintf("%s · %d×%d · %s", t.Name, t.Width, t.Height, t.FPS.Label())
}

func bt(id, name string, w, h int, r model.Rate) Template {
	return Template{ID: id, Name: name, Width: w, Height: h, FPS: r}
}

// BuiltinTemplates are the templates every installation has; the first is the factory
// default. The IDs are stable because General.DefaultTemplate stores them.
func BuiltinTemplates() []Template {
	return []Template{
		bt("1080p25", "1080p HD", 1920, 1080, model.Rate25),
		bt("1080p2398", "1080p HD", 1920, 1080, model.NTSC24),
		bt("1080p2997", "1080p HD", 1920, 1080, model.NTSC30),
		bt("1080p30", "1080p HD", 1920, 1080, model.Rate30),
		bt("1080p60", "1080p HD", 1920, 1080, model.Rate60),
		bt("2160p25", "4K UHD", 3840, 2160, model.Rate25),
		bt("2160p2997", "4K UHD", 3840, 2160, model.NTSC30),
		bt("2160p5994", "4K UHD", 3840, 2160, model.NTSC60),
		bt("720p25", "720p HD", 1280, 720, model.Rate25),
		bt("vertical30", "Social hochkant", 1080, 1920, model.Rate30),
		bt("square30", "Social quadratisch", 1080, 1080, model.Rate30),
		bt("dci4k24", "DCI 4K", 4096, 2160, model.Rate24),
		bt("pal25", "SD PAL", 720, 576, model.Rate25),
		bt("ntsc2997", "SD NTSC", 720, 480, model.NTSC30),
	}
}

// AllTemplates lists the built-in templates followed by the user templates.
func (s Settings) AllTemplates() []Template {
	out := BuiltinTemplates()
	for _, u := range s.General.Templates {
		u.User = true
		out = append(out, u)
	}
	return out
}

// FormatID is the settings ID of an arbitrary format: "fmt:1920x1080@30000/1001" with a
// "+df" suffix for drop-frame. General.DefaultTemplate holds it for the last used format.
func FormatID(w, h int, r model.Rate, drop bool) string {
	id := fmt.Sprintf("fmt:%dx%d@%s", w, h, r.Fraction())
	if drop && r.CanDropFrame() {
		id += "+df"
	}
	return id
}

var (
	fmtPattern  = regexp.MustCompile(`^(?:fmt:)?(\d+)x(\d+)@([0-9./]+)(\+df)?$`)
	heightRate  = regexp.MustCompile(`^(\d+)p(\d+(?:\.\d+)?)$`)
	errNoFormat = errors.New("Diese Vorlage gibt es nicht mehr.")
)

// ResolveTemplate finds the template of an ID: built-in, user ("user:3"), a stored format
// ("fmt:WxH@rate") or the legacy forms "WxH@fps" and "1080p25". ok is false for unknown IDs.
func (s Settings) ResolveTemplate(id string) (Template, bool) {
	id = strings.TrimSpace(id)
	for _, t := range s.AllTemplates() {
		if strings.EqualFold(t.ID, id) {
			return t, true
		}
	}
	low := strings.ToLower(id)
	if m := fmtPattern.FindStringSubmatch(low); m != nil {
		w, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		r, err := model.ParseRate(m[3])
		t := Template{ID: id, Name: "Zuletzt verwendet", Width: w, Height: h, FPS: r, DropFrame: m[4] != "" && r.CanDropFrame()}
		if err == nil && model.ValidateSettings(model.Settings{Width: w, Height: h, FPS: r}) == nil {
			return t, true
		}
		return Template{}, false
	}
	if m := heightRate.FindStringSubmatch(low); m != nil {
		h, _ := strconv.Atoi(m[1])
		r, err := model.ParseRate(m[2])
		if err == nil && h >= model.MinSize && h <= model.MaxSize/2 {
			return Template{ID: id, Name: fmt.Sprintf("%dp", h), Width: (h*16/9 + 1) &^ 1, Height: h, FPS: r}, true
		}
	}
	return Template{}, false
}

// TemplateOrDefault resolves id and falls back to the first built-in template.
func (s Settings) TemplateOrDefault(id string) Template {
	if t, ok := s.ResolveTemplate(id); ok {
		return t
	}
	return BuiltinTemplates()[0]
}

// ValidateTemplateName checks a name for a user template; skipID is the template being
// renamed.
func (s Settings) ValidateTemplateName(name, skipID string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return errors.New("Der Name der Vorlage fehlt. Bitte einen Namen eingeben, z. B. „Kino 24p“.")
	case utf8.RuneCountInString(name) > 60:
		return errors.New("Der Name ist zu lang. Bitte höchstens 60 Zeichen verwenden.")
	}
	for _, t := range s.General.Templates {
		if t.ID != skipID && strings.EqualFold(t.Name, name) {
			return fmt.Errorf("Eine Vorlage „%s“ gibt es schon. Bitte einen anderen Namen wählen.", t.Name)
		}
	}
	return nil
}

// AddTemplate stores a user template and returns it. It does not save; call it inside Update.
func AddTemplate(s *Settings, name string, w, h int, r model.Rate, drop bool) (Template, error) {
	name = strings.TrimSpace(name)
	if err := s.ValidateTemplateName(name, ""); err != nil {
		return Template{}, err
	}
	r = r.Reduced()
	if err := model.ValidateSettings(model.Settings{Width: w, Height: h, FPS: r}); err != nil {
		return Template{}, err
	}
	id := "user:" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t := Template{ID: id, Name: name, Width: w, Height: h, FPS: r, DropFrame: drop && r.CanDropFrame(), User: true}
	s.General.Templates = append(s.General.Templates, t)
	return t, nil
}

// RenameTemplate renames a user template.
func RenameTemplate(s *Settings, id, name string) error {
	name = strings.TrimSpace(name)
	if err := s.ValidateTemplateName(name, id); err != nil {
		return err
	}
	for i := range s.General.Templates {
		if s.General.Templates[i].ID == id {
			s.General.Templates[i].Name = name
			return nil
		}
	}
	return errNoFormat
}

// DeleteTemplate removes a user template. A default that pointed at it falls back to the
// factory template.
func DeleteTemplate(s *Settings, id string) error {
	for i := range s.General.Templates {
		if s.General.Templates[i].ID == id {
			s.General.Templates = append(s.General.Templates[:i:i], s.General.Templates[i+1:]...)
			if s.General.DefaultTemplate == id {
				s.General.DefaultTemplate = BuiltinTemplates()[0].ID
			}
			return nil
		}
	}
	return errNoFormat
}
