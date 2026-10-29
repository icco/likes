package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
	linkStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#265c83")).Underline(true)
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

type ratedMsg struct{ err error }

type model struct {
	ctx    context.Context
	api    api
	cs     []candidate
	i      int
	rated  int
	saving bool
	err    error
	onSkip func(candidate) error
}

func newModel(ctx context.Context, a api, cs []candidate, onSkip func(candidate) error) model {
	return model{ctx: ctx, api: a, cs: cs, onSkip: onSkip}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) done() bool { return m.i >= len(m.cs) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ratedMsg:
		m.saving = false
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.rated++
		return m.next()
	case tea.KeyPressMsg:
		if m.saving {
			return m, nil
		}
		switch k := msg.String(); k {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "enter", "space", "n":
			return m.next()
		case "s":
			if err := m.onSkip(m.cs[m.i]); err != nil {
				m.err = err
				return m, tea.Quit
			}
			return m.next()
		default:
			if len(k) == 1 && k[0] >= '0' && k[0] <= '9' {
				rating := int(k[0] - '0')
				if rating == 0 {
					rating = 10
				}
				m.saving = true
				c := m.cs[m.i]
				return m, func() tea.Msg { return ratedMsg{m.api.rate(m.ctx, c, rating)} }
			}
		}
	}
	return m, nil
}

func (m model) next() (tea.Model, tea.Cmd) {
	m.i++
	if m.done() {
		return m, tea.Quit
	}
	return m, nil
}

func (m model) View() tea.View {
	if m.done() || m.err != nil {
		return tea.NewView("")
	}
	c := m.cs[m.i]
	label := "Movie"
	if c.Kind == "shows" {
		label = "Show"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", dimStyle.Render(fmt.Sprintf("%d/%d  %s", m.i+1, len(m.cs), label)))
	fmt.Fprintf(&b, "%s %s\n", titleStyle.Render(c.Media.Title), dimStyle.Render(fmt.Sprintf("(%d), watched %s", c.Media.Year, c.Watched.Format(time.DateOnly))))
	fmt.Fprintf(&b, "%s\n\n", linkStyle.Render(fmt.Sprintf("https://trakt.tv/%s/%s", c.Kind, c.Media.IDs.Slug)))
	if m.saving {
		b.WriteString(dimStyle.Render("saving..."))
	} else {
		b.WriteString(dimStyle.Render("1-9 rate, 0 = 10, s skip for good, enter later, q quit"))
	}
	return tea.NewView(b.String())
}

func (m model) summary() string {
	out := fmt.Sprintf("Rated %d titles.", m.rated)
	if m.err != nil {
		out += "\n" + errStyle.Render(m.err.Error())
	}
	return out
}
