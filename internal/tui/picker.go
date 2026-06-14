package tui

import (
	"os"
	"path/filepath"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func (m model) openFilePicker(target pickerTarget) (tea.Model, tea.Cmd) {
	picker := filepicker.New()
	picker.CurrentDirectory = pickerStartDir(target, m.inputs)
	picker.ShowHidden = true
	picker.ShowPermissions = false
	picker.ShowSize = true
	picker.SetHeight(max(6, m.height-8))
	switch target {
	case pickerCredentials:
		picker.AllowedTypes = []string{".json"}
		picker.FileAllowed = true
		picker.DirAllowed = false
	case pickerSyncDir:
		picker.AllowedTypes = nil
		picker.FileAllowed = false
		picker.DirAllowed = false
	}
	m.picker = picker
	m.pickFor = target
	m.mode = modeFilePicker
	return m, picker.Init()
}

func pickerStartDir(target pickerTarget, inputs []textinput.Model) string {
	raw := ""
	switch target {
	case pickerCredentials:
		if len(inputs) > 1 {
			raw = inputs[1].Value()
		}
	case pickerSyncDir:
		if len(inputs) > 2 {
			raw = inputs[2].Value()
		}
	}
	raw = filepath.Clean(raw)
	if raw != "." && raw != "" {
		if info, err := os.Stat(raw); err == nil {
			if info.IsDir() {
				return raw
			}
			return filepath.Dir(raw)
		}
		if dir := filepath.Dir(raw); dir != "." {
			return dir
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
