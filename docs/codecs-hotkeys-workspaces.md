# Codecs, keyboard presets and workspaces

Video imports use FFmpeg/ffprobe, including H.264, HEVC (H.265) and AV1 in supported containers. Camera containers TS, MTS, M2TS and MXF are also accepted. AV1 joins the existing per-codec VAAPI/NVDEC decode probes; unsupported profiles continue through software decoding.

Export offers MP4 with H.264, HEVC or AV1. Under **Erweitert → Encoder**, choose Software, Automatisch, VAAPI, NVIDIA NVENC or Intel QSV. Automatic mode tests hardware first and falls back to software. Explicit hardware selections report an error when unavailable. A real one-frame encode checks the codec, dimensions and driver before project rendering starts. Software AV1 tries SVT-AV1 and then libaom. FFmpeg must have the corresponding encoder compiled in. Render progress names the encoder actually selected.

Audio clip and track faders support −60 to +60 dB. The lower endpoint is silence; 0 dB is unity. Meters retain their existing full-scale reference. Boosting audio beyond full scale can clip on export; the live master uses its existing limiter.

## Frame Studio keys

| Action | Shortcut |
| --- | --- |
| Split at playhead | Ctrl+K |
| Split all tracks | Ctrl+Shift+K |
| Ripple delete | Delete |
| Delete leaving a gap | Backspace |
| Clipboard cut / copy / paste | Ctrl+X / Ctrl+C / Ctrl+V |
| Link / unlink | Ctrl+L / Ctrl+Shift+L |
| Clip gain +1 / −1 dB | Alt+Up / Alt+Down |
| Reset clip gain | Alt+0 |
| Track gain +1 / −1 dB | Ctrl+Shift+Up / Ctrl+Shift+Down |
| Reset track gain | Ctrl+Shift+0 |
| Track mute / solo | Alt+Shift+M / Alt+Shift+S |
| Save layout | Ctrl+Alt+Shift+S |
| Manage layouts | Ctrl+Alt+Shift+W |
| Zoom timeline at pointer | Alt+wheel |
| Pan timeline in time | Ctrl+wheel |

**Tastenkürzel** includes presets for Frame Studio, DaVinci Resolve, Sony/MAGIX VEGAS Pro, Filmora and Adobe Premiere Pro. Selecting a preset replaces the current overrides; individual edits remain supported and are saved in the existing keymap settings. Presets map equivalent implemented actions, rather than claiming full emulation of another editor. Conflicting Frame Studio defaults are disabled when a preset reuses their keys. Resetting restores the Frame Studio defaults. Text inputs retain their own editing keys.

Preset references:

- [Blackmagic: Resolve advanced editing guide](https://documents.blackmagicdesign.com/UserManuals/DaVinci-Resolve-16-Advanced-Editing-p1.pdf)
- [VEGAS Pro keyboard shortcuts](https://help.magix-hub.com/video/vegas/22/en/content/topics/13-appendix/shortcutkeys.htm)
- [Filmora Windows keyboard shortcuts](https://filmora.wondershare.com/guide/keyboard-shortcuts-on-windows.html)
- [Adobe Premiere default keyboard shortcuts](https://helpx.adobe.com/premiere/desktop/get-started/keyboard-shortcuts/default-keyboard-shortcuts.html)
- [FFmpeg codec and encoder documentation](https://www.ffmpeg.org/ffmpeg-codecs.html)

## Panel docking and layouts

The shell embeds panels assigned to the main monitor in a resizable dock (below the workspace for the timeline, beside it for other panels). Each secondary monitor gets one shared panel window. Each dock can use tabs, horizontal splits or vertical splits. Panel title buttons select the keyboard target in split mode; tabs select it in tab mode. Individual panels can be closed without closing their neighbours. The edit page's original panel areas, mixer and scopes hide while docked and return on closing the dock without overwriting the user's saved visibility choices.

Each workspace page (media, edit, colour, audio, deliver) remembers its own panel/monitor arrangement. Named layouts and the last-session layout store these workspace arrangements, docking mode and divider positions. On monitor removal, its panels share the primary dock; reconnection restores their monitor assignments. Under native Wayland, positioning remains subject to compositor limitations already reported by the display backend.

The lower page bar is now a compact 34-unit horizontal icon-and-label row instead of a 52-unit stacked row.
