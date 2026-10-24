package command

// KeyPreset maps equivalent Frame Studio commands. It does not claim to implement
// commands or context-dependent gestures of another editor.
type KeyPreset struct {
	ID, Name string
	Keys     map[string]string
}

// KeyPresets are the supported desktop editing presets. Application-specific commands
// retain Frame Studio bindings; mappings to keys already in use clear those defaults.
var KeyPresets = []KeyPreset{
	{ID: "framestudio", Name: "Frame Studio", Keys: map[string]string{}},
	{ID: "resolve", Name: "DaVinci Resolve", Keys: map[string]string{
		"clip.split": "Ctrl+B", "edit.delete": "Backspace", "edit.rippleDelete": "Delete",
		"tool.select": "A", "tool.blade": "B", "timeline.snapping": "N", "timeline.zoomFit": "Shift+Z",
		"clip.link": "Ctrl+Alt+L", "edit.redo": "Ctrl+Y",
	}},
	{ID: "vegas", Name: "Sony / MAGIX VEGAS Pro", Keys: map[string]string{
		"clip.split": "S", "edit.delete": "Delete", "edit.rippleDelete": "", "edit.redo": "Ctrl+Y",
		"edit.deselect": "Ctrl+Shift+A", "edit.duplicate": "", "timeline.snapping": "F8", "clip.link": "G", "clip.unlink": "U", "audio.trackMute": "Z", "audio.trackSolo": "X",
	}},
	{ID: "filmora", Name: "Wondershare Filmora", Keys: map[string]string{
		"clip.split": "Ctrl+B", "edit.delete": "Delete", "edit.rippleDelete": "Shift+Delete", "edit.redo": "Ctrl+Y",
		"tool.select": "V", "tool.blade": "C", "edit.duplicate": "Ctrl+D", "timeline.zoomIn": "Ctrl+=", "timeline.zoomOut": "Ctrl+-",
		"deliver.export": "Ctrl+E", "view.viewerFullscreen": "Alt+Return",
	}},
	{ID: "premiere", Name: "Adobe Premiere Pro", Keys: map[string]string{
		"clip.split": "Ctrl+K", "edit.delete": "Delete", "edit.rippleDelete": "Shift+Delete",
		"tool.select": "V", "tool.blade": "C", "tool.ripple": "B", "tool.roll": "N", "timeline.snapping": "S",
		"edit.deselect": "Ctrl+Shift+A", "timeline.zoomIn": "=", "timeline.zoomOut": "-", "clip.link": "Ctrl+L",
	}},
}

// PresetKeymap returns standalone overrides, including disabled conflicting defaults.
func PresetKeymap(id string) (map[string]string, bool) {
	var preset *KeyPreset
	for i := range KeyPresets {
		if KeyPresets[i].ID == id {
			preset = &KeyPresets[i]
			break
		}
	}
	if preset == nil {
		return nil, false
	}
	result := map[string]string{}
	for command, sc := range preset.Keys {
		result[command] = sc
		if sc == "" {
			continue
		}
		for _, entry := range Catalog {
			if entry.ID != command && Normalize(entry.Shortcut) == Normalize(sc) {
				if _, mapped := preset.Keys[entry.ID]; !mapped {
					result[entry.ID] = ""
				}
			}
		}
	}
	return result, true
}
