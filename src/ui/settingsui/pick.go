package settingsui

import (
	"errors"
	"net/url"
	"os"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"github.com/rymdport/portal/filechooser"

	"framestudio-proto/src/ui/kit"
)

// portalFolder asks the desktop's file chooser (xdg-desktop-portal) for a folder. It
// blocks; "" without error means cancelled.
var portalFolder = func(title, start string) (string, error) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" && os.Getenv("XDG_RUNTIME_DIR") == "" {
		return "", errors.New("kein Desktop-Portal verfügbar")
	}
	uris, err := filechooser.OpenFile("", title, &filechooser.OpenFileOptions{
		AcceptLabel: "Wählen", Directory: true, CurrentFolder: start,
	})
	if err != nil {
		return "", err
	}
	if len(uris) == 0 {
		return "", nil
	}
	u, err := url.Parse(uris[0])
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return "", errors.New("das Portal lieferte keinen lokalen Ordner")
	}
	return u.Path, nil
}

// defaultPickFolder uses the portal off the UI thread and falls back to Fyne's folder
// dialog when no portal is available. done runs on the UI thread, never after the
// window closed.
func (w *window) defaultPickFolder(title, start string, done func(string)) {
	go func() {
		p, err := portalFolder(title, start)
		kit.Do(func() {
			if w.closed {
				return
			}
			if err == nil {
				done(p)
				return
			}
			w.fyneFolder(start, done)
		})
	}()
}

func (w *window) fyneFolder(start string, done func(string)) {
	d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil || u == nil || w.closed {
			return
		}
		done(strings.TrimPrefix(u.String(), "file://"))
	}, w.win)
	if start != "" {
		if l, err := storage.ListerForURI(storage.NewFileURI(start)); err == nil {
			d.SetLocation(l)
		}
	}
	d.SetConfirmText("Wählen")
	d.SetDismissText("Abbrechen")
	d.Resize(fyne.NewSize(760, 520))
	d.Show()
}
