package viewer

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Transport icons on a 24 px grid, fill only so the theme can recolour them.
func svgIcon(name, body string) fyne.Resource {
	return fyne.NewStaticResource(name+".svg",
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">`+body+`</svg>`))
}

var (
	iconToStart = svgIcon("to-start",
		`<rect x="4" y="6" width="2" height="12" fill="#fff"/><polygon points="13,6 6.5,12 13,18" fill="#fff"/><polygon points="20,6 13.5,12 20,18" fill="#fff"/>`)
	iconPrevEdit = svgIcon("prev-edit",
		`<rect x="4" y="5" width="2" height="14" fill="#fff"/><polygon points="7.5,12 12.5,7.5 12.5,16.5" fill="#fff"/><rect x="12" y="11" width="8" height="2" fill="#fff"/>`)
	iconStepBack = svgIcon("step-back",
		`<polygon points="14.5,6 7,12 14.5,18" fill="#fff"/><rect x="16" y="6" width="2" height="12" fill="#fff"/>`)
	iconStepFwd = svgIcon("step-fwd",
		`<rect x="6" y="6" width="2" height="12" fill="#fff"/><polygon points="9.5,6 17,12 9.5,18" fill="#fff"/>`)
	iconNextEdit = svgIcon("next-edit",
		`<rect x="4" y="11" width="8" height="2" fill="#fff"/><polygon points="16.5,12 11.5,7.5 11.5,16.5" fill="#fff"/><rect x="18" y="5" width="2" height="14" fill="#fff"/>`)
	iconToEnd = svgIcon("to-end",
		`<polygon points="4,6 10.5,12 4,18" fill="#fff"/><polygon points="11,6 17.5,12 11,18" fill="#fff"/><rect x="18" y="6" width="2" height="12" fill="#fff"/>`)
	iconLoop = svgIcon("loop",
		`<rect x="3" y="7" width="14" height="2" fill="#fff"/><polygon points="16,4 20.5,8 16,12" fill="#fff"/><rect x="3" y="7" width="2" height="5" fill="#fff"/>`+
			`<rect x="7" y="15" width="14" height="2" fill="#fff"/><polygon points="8,12 3.5,16 8,20" fill="#fff"/><rect x="19" y="12" width="2" height="5" fill="#fff"/>`)
)

func themed(r fyne.Resource) fyne.Resource { return theme.NewThemedResource(r) }

func accent(r fyne.Resource) fyne.Resource { return theme.NewPrimaryThemedResource(r) }
