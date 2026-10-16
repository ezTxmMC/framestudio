package application

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/widget"
)

func Start() {
	app := app.New()
	window := app.NewWindow("Frame Studio")

	window.SetContent(widget.NewLabel("Hi from Frame Studio"))
	window.Resize(fyne.Size{Width: 1600, Height: 900})

	// Menu bar items configuration
	var items []*fyne.Menu
	var subItems []*fyne.MenuItem
	subItems = append(subItems, &fyne.MenuItem{Label: "Test"})
	items = append(items, &fyne.Menu{Label: "Hi", Items: subItems})
	window.SetMainMenu(&fyne.MainMenu{Items: items})

	window.ShowAndRun()
}
