package internal

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Level int

const (
	Info Level = iota
	Error
)

// TextFlash is used to temporarily display a message to the user.
type TextFlash struct {
	text  string
	level Level
}

type UpdateTextFlashMsg struct {
	text     string
	level    Level
	duration time.Duration

	isResetMsg bool
}

func (t TextFlash) Init() tea.Cmd {
	return nil
}

func (t TextFlash) Update(msg tea.Msg) (TextFlash, tea.Cmd) {
	switch msg := msg.(type) {
	case UpdateTextFlashMsg:
		if msg.isResetMsg {
			t.text = ""
			t.level = Info
			return t, nil
		}

		// Workaround so that existing error messages take priority over new info messages,
		// which is needed since tea.Batch(infoCmd, errorCmd) creates a race condition
		if t.level == Error && msg.level == Info {
			return t, nil
		}

		t.text = msg.text
		t.level = msg.level
		cmd := tea.Tick(msg.duration, func(time.Time) tea.Msg {
			return UpdateTextFlashMsg{
				isResetMsg: true,
			}
		})
		return t, cmd
	}
	return t, nil
}

func (t TextFlash) Flash(text string, level Level, duration time.Duration) tea.Cmd {
	return func() tea.Msg {
		return UpdateTextFlashMsg{
			text:     text,
			level:    level,
			duration: duration,
		}
	}
}

func (t TextFlash) View() string {
	if t.text == "" {
		return ""
	}

	style := lipgloss.NewStyle().
		Bold(true).
		MarginLeft(1)

	switch t.level {
	case Info:
		return style.Render("ℹ️  " + t.text)
	case Error:
		return style.Render("⚠️  " + t.text + " ⚠️")
	default:
		return style.Render(t.text)
	}
}
