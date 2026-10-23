// Package command is the central registry of user actions: every menu item, keyboard
// shortcut and palette entry is a command with a stable ID. Pages, panels and the shell
// bind the IDs of the catalog (ids.go) to actions; the menu bar, command palette and
// keymap editor read the registry. UI thread only.
package command

import (
	"sort"
	"strings"

	"fyne.io/fyne/v2"
)

// Info describes a command independent of who implements it.
type Info struct {
	ID       string
	Label    string
	Category string
	Shortcut string // default, e.g. "Ctrl+Shift+S", "Space", "J"; empty for none
}

// Binding attaches behaviour to a command ID.
type Binding struct {
	ID      string
	Run     func()
	Enabled func() bool // nil means always enabled
	Checked func() bool // nil means not a toggle
	Icon    fyne.Resource
	// Info overrides or supplies label/category/shortcut for IDs outside the catalog.
	Info *Info
}

// Command is a resolved, currently registered command.
type Command struct {
	Info
	Icon      fyne.Resource
	Toggle    bool
	Checked   bool
	Enabled   bool
	Available bool // a binding exists in an active scope
}

// Global is the scope of bindings that are always active.
const Global = ""

// PageScope returns the scope name for a workspace page, e.g. "page:edit".
func PageScope(pageID string) string { return "page:" + pageID }

type binding struct {
	Binding
	scope string
}

type Registry struct {
	catalog   map[string]Info
	order     []string
	bindings  map[string][]binding // per ID, later registrations win within active scopes
	active    map[string]bool
	keymap    map[string]string // user overrides: ID -> shortcut ("" disables)
	dynamic   map[string]func() []Item
	listeners map[int]func()
	next      int
}

// NewRegistry returns a registry preloaded with the catalog.
func NewRegistry() *Registry {
	r := &Registry{
		catalog:   map[string]Info{},
		bindings:  map[string][]binding{},
		active:    map[string]bool{Global: true},
		keymap:    map[string]string{},
		dynamic:   map[string]func() []Item{},
		listeners: map[int]func(){},
	}
	for _, in := range Catalog {
		r.catalog[in.ID] = in
		r.order = append(r.order, in.ID)
	}
	return r
}

// Bind registers bindings in scope. Rebinding an ID in the same scope replaces it.
func (r *Registry) Bind(scope string, bs ...Binding) {
	for _, b := range bs {
		if b.Info != nil {
			in := *b.Info
			in.ID = b.ID
			if _, known := r.catalog[b.ID]; !known {
				r.order = append(r.order, b.ID)
			}
			if old, ok := r.catalog[b.ID]; ok {
				if in.Label == "" {
					in.Label = old.Label
				}
				if in.Category == "" {
					in.Category = old.Category
				}
				if in.Shortcut == "" {
					in.Shortcut = old.Shortcut
				}
			}
			r.catalog[b.ID] = in
		}
		list := r.bindings[b.ID]
		for i := range list {
			if list[i].scope == scope {
				list = append(list[:i], list[i+1:]...)
				break
			}
		}
		r.bindings[b.ID] = append(list, binding{b, scope})
	}
	r.changed()
}

// Unbind removes every binding of a scope (e.g. when a project tab closes).
func (r *Registry) Unbind(scope string) {
	for id, list := range r.bindings {
		kept := list[:0]
		for _, b := range list {
			if b.scope != scope {
				kept = append(kept, b)
			}
		}
		r.bindings[id] = kept
	}
	r.changed()
}

// SetScopeActive enables or disables a scope's bindings.
func (r *Registry) SetScopeActive(scope string, on bool) {
	if r.active[scope] == on {
		return
	}
	if on {
		r.active[scope] = true
	} else {
		delete(r.active, scope)
	}
	r.changed()
}

// SetDynamic registers a generator for a dynamic submenu (e.g. recent files, layouts).
func (r *Registry) SetDynamic(id string, gen func() []Item) {
	r.dynamic[id] = gen
	r.changed()
}

// DynamicItems returns the current items of a dynamic submenu.
func (r *Registry) DynamicItems(id string) []Item {
	if gen := r.dynamic[id]; gen != nil {
		return gen()
	}
	return nil
}

// current resolves the binding of id: an active page scope wins over any other active
// scope, which wins over Global; within the same rank the later registration wins. So a
// page's own binding of an ID the shell also binds globally (play.toggle on Ton,
// edit.selectAll on Medien) applies while the page is visible, whatever the bind order.
func (r *Registry) current(id string) (binding, bool) {
	list := r.bindings[id]
	best, rank := -1, -1
	for i := len(list) - 1; i >= 0; i-- {
		sc := list[i].scope
		if !r.active[sc] {
			continue
		}
		k := 0
		switch {
		case strings.HasPrefix(sc, "page:"):
			k = 2
		case sc != Global:
			k = 1
		}
		if k > rank {
			best, rank = i, k
		}
	}
	if best < 0 {
		return binding{}, false
	}
	return list[best], true
}

// Get resolves a command. Unknown IDs return ok=false.
func (r *Registry) Get(id string) (Command, bool) {
	in, ok := r.catalog[id]
	if !ok {
		return Command{}, false
	}
	in.Shortcut = r.Shortcut(id)
	c := Command{Info: in}
	if b, ok := r.current(id); ok {
		c.Available = true
		c.Icon = b.Icon
		c.Enabled = b.Run != nil && (b.Enabled == nil || b.Enabled())
		if b.Checked != nil {
			c.Toggle = true
			c.Checked = b.Checked()
		}
	}
	return c, true
}

// Run executes id if it is available and enabled.
func (r *Registry) Run(id string) bool {
	b, ok := r.current(id)
	if !ok || b.Run == nil || (b.Enabled != nil && !b.Enabled()) {
		return false
	}
	b.Run()
	r.notify()
	return true
}

// All returns every known command in catalog order (for the keymap editor).
func (r *Registry) All() []Command {
	out := make([]Command, 0, len(r.order))
	for _, id := range r.order {
		c, _ := r.Get(id)
		out = append(out, c)
	}
	return out
}

// Available returns the commands that can run now, sorted by category and label (palette).
func (r *Registry) Available() []Command {
	var out []Command
	for _, c := range r.All() {
		if c.Available {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// Shortcut returns the effective shortcut of id (user override or default).
func (r *Registry) Shortcut(id string) string {
	if sc, ok := r.keymap[id]; ok {
		return sc
	}
	return r.catalog[id].Shortcut
}

// SetShortcut overrides the shortcut of id ("" removes it). Passing the default resets it.
func (r *Registry) SetShortcut(id, sc string) {
	sc = Normalize(sc)
	if sc == Normalize(r.catalog[id].Shortcut) {
		delete(r.keymap, id)
	} else {
		r.keymap[id] = sc
	}
	r.changed()
}

// Keymap returns the user overrides, for saving in settings.
func (r *Registry) Keymap() map[string]string {
	out := map[string]string{}
	for k, v := range r.keymap {
		out[k] = v
	}
	return out
}

// SetKeymap replaces all user overrides.
func (r *Registry) SetKeymap(m map[string]string) {
	r.keymap = map[string]string{}
	for k, v := range m {
		r.keymap[k] = Normalize(v)
	}
	r.changed()
}

// Conflicts returns the IDs (other than except) whose effective shortcut equals sc.
func (r *Registry) Conflicts(sc, except string) []string {
	sc = Normalize(sc)
	if sc == "" {
		return nil
	}
	var ids []string
	for _, id := range r.order {
		if id != except && Normalize(r.Shortcut(id)) == sc {
			ids = append(ids, id)
		}
	}
	return ids
}

// Dispatch runs the available, enabled command bound to the key combination.
// Page-scoped bindings win over global ones. Returns true when a command ran.
func (r *Registry) Dispatch(key fyne.KeyName, mods fyne.KeyModifier) bool {
	sc := Format(key, mods)
	if sc == "" {
		return false
	}
	var global string
	for _, id := range r.order {
		if Normalize(r.Shortcut(id)) != sc {
			continue
		}
		b, ok := r.current(id)
		if !ok || b.Run == nil || (b.Enabled != nil && !b.Enabled()) {
			continue
		}
		if b.scope != Global {
			return r.Run(id)
		}
		if global == "" {
			global = id
		}
	}
	if global != "" {
		return r.Run(global)
	}
	return false
}

// Subscribe is called after registrations, scope switches, keymap changes and runs
// (menus refresh their check marks and enabled state).
func (r *Registry) Subscribe(fn func()) (unsubscribe func()) {
	id := r.next
	r.next++
	r.listeners[id] = fn
	return func() { delete(r.listeners, id) }
}

// Refresh tells subscribers that enabled/checked state may have changed.
func (r *Registry) Refresh() { r.notify() }

func (r *Registry) changed() { r.notify() }

func (r *Registry) notify() {
	for _, fn := range r.listeners {
		fn()
	}
}

// Matches reports whether a palette query matches a command (all words, any order).
func Matches(c Command, query string) bool {
	hay := strings.ToLower(c.Label + " " + c.Category + " " + c.ID)
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}
