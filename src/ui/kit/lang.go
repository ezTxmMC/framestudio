package kit

import (
	"fyne.io/fyne/v2/lang"
)

// Fyne's German bundle lacks the select placeholder and logs an error for every
// widget.Select; supply it once for the app and all UI tests.
func init() {
	_ = lang.AddTranslationsForLocale([]byte(`{"(Select one)": "(Auswählen)"}`), "de")
}
