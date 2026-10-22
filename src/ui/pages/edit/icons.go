package edit

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Toolbar icons on a 24 px grid, fill only so the theme can recolour them.
func svgIcon(name, body string) fyne.Resource {
	return fyne.NewStaticResource(name+".svg",
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">`+body+`</svg>`))
}

var (
	iconSelect = svgIcon("select",
		`<polygon points="6,3 6,19.5 10.2,15.6 13,21.5 15.6,20.3 12.9,14.5 18.5,14.5" fill="#fff"/>`)
	iconBlade = svgIcon("blade",
		`<polygon points="3.5,17.5 14.5,6.5 17.5,9.5 6.5,20.5" fill="#fff"/><polygon points="15.5,5.5 21,3 18.5,8.5" fill="#fff"/>`)
	iconSplit = svgIcon("split",
		`<rect x="2.5" y="8" width="7.5" height="8" rx="1" fill="#fff"/><rect x="14" y="8" width="7.5" height="8" rx="1" fill="#fff"/><rect x="11.25" y="3" width="1.5" height="18" fill="#fff"/>`)
	iconRipple = svgIcon("ripple",
		`<rect x="2" y="7" width="6" height="10" rx="1" fill="#fff"/><rect x="16" y="7" width="6" height="10" rx="1" fill="#fff"/><polygon points="9.5,12 14,8.5 14,15.5" fill="#fff"/>`)
	iconMarker = svgIcon("marker",
		`<polygon points="6.5,4 17.5,4 17.5,14 12,19.5 6.5,14" fill="#fff"/>`)
	iconIn = svgIcon("mark-in",
		`<rect x="8" y="4" width="2.2" height="16" fill="#fff"/><rect x="8" y="4" width="8" height="2.2" fill="#fff"/><rect x="8" y="17.8" width="8" height="2.2" fill="#fff"/>`)
	iconOut = svgIcon("mark-out",
		`<rect x="13.8" y="4" width="2.2" height="16" fill="#fff"/><rect x="8" y="4" width="8" height="2.2" fill="#fff"/><rect x="8" y="17.8" width="8" height="2.2" fill="#fff"/>`)
	iconClearRange = svgIcon("clear-range",
		`<rect x="2.5" y="5" width="2" height="14" fill="#fff"/><rect x="2.5" y="5" width="4" height="2" fill="#fff"/><rect x="2.5" y="17" width="4" height="2" fill="#fff"/>`+
			`<rect x="19.5" y="5" width="2" height="14" fill="#fff"/><rect x="17.5" y="5" width="4" height="2" fill="#fff"/><rect x="17.5" y="17" width="4" height="2" fill="#fff"/>`+
			`<polygon points="8,9.4 9.4,8 16,14.6 14.6,16" fill="#fff"/><polygon points="14.6,8 16,9.4 9.4,16 8,14.6" fill="#fff"/>`)
	iconAddVideo = svgIcon("add-video",
		`<rect x="2" y="5.5" width="10.5" height="9" rx="1.5" fill="#fff"/><polygon points="13,10 17,7.2 17,12.8" fill="#fff"/>`+
			`<rect x="17" y="13.5" width="2" height="8" fill="#fff"/><rect x="14" y="16.5" width="8" height="2" fill="#fff"/>`)
	iconAddAudio = svgIcon("add-audio",
		`<rect x="2.5" y="7.5" width="3.5" height="6" fill="#fff"/><polygon points="6,7.5 11,3.5 11,17.5 6,13.5" fill="#fff"/>`+
			`<rect x="17" y="13.5" width="2" height="8" fill="#fff"/><rect x="14" y="16.5" width="8" height="2" fill="#fff"/>`)
	iconRemoveTrack = svgIcon("remove-track",
		`<rect x="2" y="5" width="12" height="3" rx="0.8" fill="#fff"/><rect x="2" y="10.5" width="12" height="3" rx="0.8" fill="#fff"/><rect x="2" y="16" width="12" height="3" rx="0.8" fill="#fff"/>`+
			`<rect x="15.5" y="10.8" width="7" height="2.4" fill="#fff"/>`)
	iconSnap = svgIcon("snap",
		`<path d="M5 3h4.5v9a2.5 2.5 0 0 0 5 0V3H19v9a7 7 0 0 1-14 0z" fill="#fff"/>`)
)

func themed(r fyne.Resource) fyne.Resource { return theme.NewThemedResource(r) }

func accent(r fyne.Resource) fyne.Resource { return theme.NewPrimaryThemedResource(r) }
