// Package assets embeds the fonts shared by the UI theme and the renderer.
package assets

import _ "embed"

//go:embed fonts/RobotoCondensed-Regular.ttf
var FontRegular []byte

//go:embed fonts/RobotoCondensed-Bold.ttf
var FontBold []byte

//go:embed fonts/RobotoCondensed-Italic.ttf
var FontItalic []byte

//go:embed fonts/JetBrainsMono-Medium.ttf
var FontMono []byte
