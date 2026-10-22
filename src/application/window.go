package application

import (
	"fyne.io/fyne/v2/app"

	"framestudio-proto/src/ui/shell"
	studiotheme "framestudio-proto/src/ui/theme"
)

// Run starts Frame Studio, opening any project files given on the command line.
func Run(files []string) {
	a := app.NewWithID("de.framestudio.proto")
	a.Settings().SetTheme(studiotheme.New())
	shell.Run(a, files)
}
