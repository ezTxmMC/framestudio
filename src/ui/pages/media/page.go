// Package media is the "media" workspace page.
//
// OWNER: media page agent. New(ctx) is the contract; do not change its signature.
package media

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/page"
)

type Page struct{ ctx *page.Context }

func New(ctx *page.Context) page.Page { return &Page{ctx: ctx} }

func (p *Page) Content() fyne.CanvasObject   { return widget.NewLabel("media (TODO)") }
func (p *Page) Activate()                    {}
func (p *Page) Deactivate()                  {}
func (p *Page) TypedKey(*fyne.KeyEvent) bool { return false }
func (p *Page) Close()                       {}
