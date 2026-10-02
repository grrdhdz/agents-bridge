// Package keys is the TUI's single, central keymap (spec §3, §6.6): every
// shortcut is declared exactly once here, grouped by the context it applies
// in, and the shortcuts bar / help overlay are both generated from it so
// they can never drift from what Update actually handles.
package keys

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
)

const (
	globalContext       = "Global"
	conversationContext = "Conversación"
	homeContext         = "Inicio"
	composerContext     = "Composer"
)

// GlobalKeys apply regardless of which pane has focus.
type GlobalKeys struct {
	FocusToggle   key.Binding
	HelpToggle    key.Binding
	Quit          key.Binding
	SidebarToggle key.Binding
	Search        key.Binding
	Palette       key.Binding
	// Home returns to the bridge list (only when the bridge was entered
	// from the home screen; the Model disables it otherwise).
	Home key.Binding
}

// HomeKeys apply on the home screen (the list of live bridges, spec §5).
type HomeKeys struct {
	Up      key.Binding
	Down    key.Binding
	Enter   key.Binding
	Stop    key.Binding
	New     key.Binding
	Refresh key.Binding
	Filter  key.Binding
	Palette key.Binding
	Help    key.Binding
	Quit    key.Binding
}

// ConversationKeys apply only while the conversation pane has focus. Up,
// Down, Top and Bottom drive the message *selection* (§6.3 "Selección"),
// not raw viewport scrolling; PageUp/PageDown still scroll the viewport
// directly for skimming a long history.
type ConversationKeys struct {
	Up         key.Binding
	Down       key.Binding
	Top        key.Binding
	Bottom     key.Binding
	PageUp     key.Binding
	PageDown   key.Binding
	Help       key.Binding
	ToggleFold key.Binding
	Copy       key.Binding
	SearchNext key.Binding
	SearchPrev key.Binding
}

// ComposerKeys apply only while the composer has focus.
type ComposerKeys struct {
	Send        key.Binding
	Newline     key.Binding
	CycleLabel  key.Binding
	HistoryUp   key.Binding
	HistoryDown key.Binding
}

// Map is the whole keymap. It is small enough to pass by value.
type Map struct {
	Global       GlobalKeys
	Conversation ConversationKeys
	Composer     ComposerKeys
	Home         HomeKeys
}

// New builds the keymap. There is exactly one instance in the whole
// program; components read from it, they never declare their own bindings.
func New() Map {
	return Map{
		Global: GlobalKeys{
			FocusToggle:   key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "cambiar foco")),
			HelpToggle:    key.NewBinding(key.WithKeys("ctrl+/"), key.WithHelp("ctrl+/", "ayuda")),
			Quit:          key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "salir")),
			SidebarToggle: key.NewBinding(key.WithKeys("ctrl+b"), key.WithHelp("ctrl+b", "panel lateral")),
			Search:        key.NewBinding(key.WithKeys("ctrl+f"), key.WithHelp("ctrl+f", "buscar")),
			Palette:       key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "paleta")),
			Home:          key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "volver a inicio")),
		},
		Conversation: ConversationKeys{
			Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "mensaje anterior")),
			Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "mensaje siguiente")),
			Top:        key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g/home", "primer mensaje")),
			Bottom:     key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G/end", "último mensaje")),
			PageUp:     key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "página arriba")),
			PageDown:   key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdown", "página abajo")),
			Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "ayuda")),
			ToggleFold: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "plegar/desplegar")),
			Copy:       key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copiar mensaje")),
			SearchNext: key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "siguiente coincidencia")),
			SearchPrev: key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "coincidencia anterior")),
		},
		Composer: ComposerKeys{
			Send:        key.NewBinding(key.WithKeys("ctrl+s", "ctrl+enter"), key.WithHelp("ctrl+s/ctrl+enter", "enviar")),
			Newline:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "línea nueva")),
			CycleLabel:  key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "rotar etiqueta")),
			HistoryUp:   key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "historial anterior")),
			HistoryDown: key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "historial siguiente")),
		},
		Home: HomeKeys{
			Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "puente anterior")),
			Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "puente siguiente")),
			Enter:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "entrar")),
			Stop:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "cerrar puente")),
			New:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "crear puente")),
			Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refrescar")),
			Filter:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filtrar")),
			Palette: key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "paleta")),
			Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "ayuda")),
			Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "salir")),
		},
	}
}

// Group is one named context's list of bindings, used for both the
// collision check and the generated help.
type Group struct {
	Name     string
	Bindings []key.Binding
}

// Groups returns every context in the keymap, in display order.
func (m Map) Groups() []Group {
	return []Group{
		{Name: globalContext, Bindings: []key.Binding{
			m.Global.FocusToggle, m.Global.HelpToggle, m.Global.Quit, m.Global.Home,
			m.Global.SidebarToggle, m.Global.Search, m.Global.Palette,
		}},
		{Name: conversationContext, Bindings: []key.Binding{
			m.Conversation.Up, m.Conversation.Down, m.Conversation.Top, m.Conversation.Bottom,
			m.Conversation.PageUp, m.Conversation.PageDown, m.Conversation.Help,
			m.Conversation.ToggleFold, m.Conversation.Copy, m.Conversation.SearchNext, m.Conversation.SearchPrev,
		}},
		{Name: composerContext, Bindings: []key.Binding{
			m.Composer.Send, m.Composer.Newline, m.Composer.CycleLabel, m.Composer.HistoryUp, m.Composer.HistoryDown,
		}},
		{Name: homeContext, Bindings: []key.Binding{
			m.Home.Enter, m.Home.New, m.Home.Stop, m.Home.Refresh, m.Home.Filter,
			m.Home.Palette, m.Home.Quit, m.Home.Up, m.Home.Down, m.Home.Help,
		}},
	}
}

// Help renders the full help overlay (§6.6), grouped by context, generated
// straight from the keymap so it can never omit or misname a shortcut.
func (m Map) Help() string { return m.HelpFor() }

// HelpFor is Help restricted to the named contexts (none means all): each
// screen shows only the contexts that apply to it. Disabled bindings (esc
// to go home in a bridge that was not entered from the home screen) are
// left out.
func (m Map) HelpFor(groupNames ...string) string {
	want := map[string]bool{}
	for _, name := range groupNames {
		want[name] = true
	}
	var b strings.Builder
	first := true
	for _, group := range m.Groups() {
		if len(want) > 0 && !want[group.Name] {
			continue
		}
		if !first {
			b.WriteString("\n\n")
		}
		first = false
		fmt.Fprintf(&b, "%s\n", group.Name)
		for _, binding := range group.Bindings {
			if !binding.Enabled() {
				continue
			}
			h := binding.Help()
			fmt.Fprintf(&b, "  %-20s %s\n", h.Key, h.Desc)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Bar renders §3's amended shortcuts bar: always a single line, showing as
// many bindings as fit — highest priority (declaration order within the
// requested groups) first — and always ending in "? más" so the full help
// is discoverable regardless of how narrow the terminal is. extra are
// pre-rendered "key desc" entries (e.g. a mode-specific f5 hint) appended
// after the keymap's own bindings, at the lowest priority.
func (m Map) Bar(width int, extra []string, groupNames ...string) string {
	const more = "? más"
	const sep = " · "
	if width <= len(more) {
		return more
	}
	budget := width - len(more) - len(sep)

	want := map[string]bool{}
	for _, name := range groupNames {
		want[name] = true
	}
	var entries []string
	for _, group := range m.Groups() {
		if len(want) > 0 && !want[group.Name] {
			continue
		}
		for _, binding := range group.Bindings {
			if !binding.Enabled() {
				continue
			}
			h := binding.Help()
			entries = append(entries, h.Key+" "+h.Desc)
		}
	}
	entries = append(entries, extra...)

	var kept []string
	used := 0
	for _, entry := range entries {
		add := len(entry)
		if used > 0 {
			add += len(sep)
		}
		if used+add > budget {
			break
		}
		used += add
		kept = append(kept, entry)
	}
	if len(kept) == 0 {
		return more
	}
	return strings.Join(kept, sep) + sep + more
}

// ShortHelp renders the compact shortcuts bar (§6.1's "atajos" strip):
// one "key desc" pair per binding across every context, in declaration
// order, joined with " · " like the previous single-line footer.
func (m Map) ShortHelp(groupNames ...string) string {
	want := map[string]bool{}
	for _, name := range groupNames {
		want[name] = true
	}
	var parts []string
	for _, group := range m.Groups() {
		if len(want) > 0 && !want[group.Name] {
			continue
		}
		for _, binding := range group.Bindings {
			h := binding.Help()
			parts = append(parts, h.Key+" "+h.Desc)
		}
	}
	return strings.Join(parts, " · ")
}
