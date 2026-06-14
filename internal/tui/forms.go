package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"

	"tasks-remote/internal/storage"
	"tasks-remote/internal/syncsetup"
)

func newTextInput(placeholder string) textinput.Model {
	input := textinput.New()
	input.Placeholder = placeholder
	input.Prompt = "> "
	input.CharLimit = 512
	input.Width = 48
	input.Focus()
	return input
}

func newSecretInput(placeholder string) textinput.Model {
	input := newTextInput(placeholder)
	input.EchoMode = textinput.EchoPassword
	input.EchoCharacter = '*'
	return input
}

func taskFormInputs(task storage.Task) []textinput.Model {
	inputs := []textinput.Model{
		newTextInput("Title"),
		newTextInput("Body"),
		newTextInput("Tags: comma separated"),
		newTextInput("Due: YYYY-MM-DD or RFC3339"),
		newTextInput("Reminder: YYYY-MM-DD or RFC3339"),
	}
	inputs[0].SetValue(task.Title)
	inputs[1].SetValue(task.Body)
	inputs[2].SetValue(strings.Join(task.Tags, ", "))
	if task.DueAt != nil {
		inputs[3].SetValue(task.DueAt.Format("2006-01-02"))
	}
	if task.ReminderAt != nil {
		inputs[4].SetValue(task.ReminderAt.Format("2006-01-02"))
	}
	for i := 1; i < len(inputs); i++ {
		inputs[i].Blur()
	}
	return inputs
}

func syncSetupInputs(cfg syncsetup.Config) []textinput.Model {
	kind := newTextInput("google or dir")
	if cfg.Kind != syncsetup.None {
		kind.SetValue(string(cfg.Kind))
	} else {
		kind.SetValue(string(syncsetup.Google))
	}
	creds := newTextInput("Google credentials JSON path")
	creds.SetValue(cfg.CredentialsPath)
	dir := newTextInput("Local sync directory")
	dir.SetValue(cfg.Dir)
	creds.Blur()
	dir.Blur()
	return []textinput.Model{kind, creds, dir}
}

func renderInputs(inputs []textinput.Model, focus int) string {
	var b strings.Builder
	for i, input := range inputs {
		if i == focus {
			b.WriteString("> ")
		} else {
			b.WriteString("  ")
		}
		b.WriteString(input.View())
		b.WriteString("\n")
	}
	return b.String()
}
