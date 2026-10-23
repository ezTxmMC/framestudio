package settingsui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Icons the default theme lacks, drawn on the same 24x24 grid and recoloured by the theme.
var (
	iconKeyboard = theme.NewThemedResource(fyne.NewStaticResource("keyboard.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M20 5H4c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V7c0-1.1-.9-2-2-2zm-9 3h2v2h-2V8zm0 3h2v2h-2v-2zM8 8h2v2H8V8zm0 3h2v2H8v-2zm-1 2H5v-2h2v2zm0-3H5V8h2v2zm9 7H8v-2h8v2zm0-4h-2v-2h2v2zm0-3h-2V8h2v2zm3 3h-2v-2h2v2zm0-3h-2V8h2v2z"/></svg>`)))
	iconTimeline = theme.NewThemedResource(fyne.NewStaticResource("timeline.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M3 5h9v4H3zM13 5h8v4h-8zM3 10.5h5v4H3zM9 10.5h12v4H9zM3 16h12v4H3zM16 16h5v4h-5z"/></svg>`)))
	iconMonitors = theme.NewThemedResource(fyne.NewStaticResource("monitors.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M1 6h7v5.5H1zM8.8 4.5h6.4v7H8.8zM16 6h7v5.5h-7zM2 12.5h5v1H2zM9.5 12.5h5v1h-5zM17 12.5h5v1h-5zM11.2 13.5h1.6V17h-1.6zM8 17h8v1.5H8z"/></svg>`)))
)
