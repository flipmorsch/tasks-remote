package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"tasks-remote/internal/app"
	"tasks-remote/internal/storage"
	"tasks-remote/internal/syncsetup"
)

type Options struct {
	DBPath string
}

func Run(ctx context.Context, opts Options) error {
	if opts.DBPath == "" {
		return fmt.Errorf("database path is required")
	}
	model := newModel(ctx, opts.DBPath)
	program := tea.NewProgram(model, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func IsInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

type mode int

const (
	modeLoading mode = iota
	modeSetup
	modeLocked
	modeUnlock
	modeCreateSecret
	modeCreateConfirm
	modeWork
	modeForm
	modeDetail
	modeSearch
	modeHelp
	modeSyncSetup
	modeFilePicker
	modeRestoreSecret
	modeRestoreConfirm
	modeDeleteConfirm
	modeConflicts
	modeConflictDetail
)

type filterMode int

const (
	filterWorking filterMode = iota
	filterAll
	filterDone
	filterSearch
)

type model struct {
	ctx     context.Context
	service app.Service
	mode    mode

	width  int
	height int

	tasks     []storage.Task
	conflicts []storage.ConflictDetail
	status    storage.SyncStatus
	config    syncsetup.Config
	unlocked  bool

	filter      filterMode
	selected    int
	conflictSel int
	err         string
	notice      string
	busy        string
	searchQuery string

	inputs    []textinput.Model
	picker    filepicker.Model
	pickFor   pickerTarget
	focus     int
	formID    string
	secret    string
	restoreOp bool
	pendingOp string
}

type pickerTarget int

const (
	pickerNone pickerTarget = iota
	pickerCredentials
	pickerSyncDir
)

type loadedMsg struct {
	mode   mode
	tasks  []storage.Task
	status storage.SyncStatus
	config syncsetup.Config
	err    error
}

type tasksMsg struct {
	tasks  []storage.Task
	status storage.SyncStatus
	err    error
}

type opMsg struct {
	notice string
	err    error
}

type conflictsMsg struct {
	conflicts []storage.ConflictDetail
	err       error
}

func newModel(ctx context.Context, dbPath string) model {
	return model{
		ctx:     ctx,
		service: app.Service{DBPath: dbPath},
		mode:    modeLoading,
		filter:  filterWorking,
	}
}

func (m model) Init() tea.Cmd {
	return m.load()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.picker.Height > 0 {
			m.picker.SetHeight(max(6, msg.Height-8))
		}
		return m, nil
	case loadedMsg:
		m.busy = ""
		m.config = msg.config
		if msg.err != nil {
			m.err = msg.err.Error()
			m.mode = msg.mode
			return m, nil
		}
		m.mode = msg.mode
		m.unlocked = msg.mode == modeWork
		m.tasks = msg.tasks
		m.status = msg.status
		return m, nil
	case tasksMsg:
		m.busy = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.tasks = msg.tasks
		m.status = msg.status
		m.clampSelection()
		return m, nil
	case conflictsMsg:
		m.busy = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.conflicts = msg.conflicts
		m.mode = modeConflicts
		if m.conflictSel >= len(m.conflicts) {
			m.conflictSel = max(0, len(m.conflicts)-1)
		}
		return m, nil
	case opMsg:
		m.busy = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.notice = msg.notice
		m.err = ""
		switch m.mode {
		case modeForm, modeDeleteConfirm, modeConflictDetail, modeRestoreConfirm:
			m.mode = modeWork
			m.unlocked = true
		case modeSyncSetup:
			if m.unlocked {
				m.mode = modeWork
			} else if m.status.Initialized {
				m.mode = modeLocked
			} else {
				m.mode = modeSetup
			}
		}
		if !m.unlocked && m.mode != modeWork {
			return m, nil
		}
		return m, m.refresh()
	case tea.KeyMsg:
		if m.busy != "" {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}
		if m.mode == modeFilePicker {
			return m.handleFilePickerKey(msg)
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if key == "esc" {
		m.err = ""
		switch m.mode {
		case modeHelp, modeDetail, modeSearch, modeSyncSetup, modeFilePicker, modeConflicts, modeConflictDetail:
			m.mode = modeWork
			return m, nil
		case modeForm:
			m.mode = modeWork
			return m, nil
		case modeUnlock:
			m.mode = modeLocked
			return m, nil
		case modeCreateSecret, modeCreateConfirm, modeRestoreSecret, modeRestoreConfirm:
			m.mode = modeSetup
			return m, nil
		case modeDeleteConfirm:
			m.mode = modeWork
			return m, nil
		}
	}
	if key == "?" {
		m.mode = modeHelp
		return m, nil
	}
	if key == "e" && m.err != "" {
		m.err = ""
		return m, nil
	}
	switch m.mode {
	case modeSetup:
		return m.handleSetupKey(key)
	case modeLocked:
		return m.handleLockedKey(key)
	case modeUnlock, modeCreateSecret, modeCreateConfirm, modeSearch, modeSyncSetup, modeRestoreSecret, modeRestoreConfirm:
		return m.handleInputKey(msg)
	case modeWork:
		return m.handleWorkKey(key)
	case modeForm:
		return m.handleFormKey(msg)
	case modeDetail:
		return m.handleDetailKey(key)
	case modeDeleteConfirm:
		return m.handleDeleteKey(key)
	case modeConflicts:
		return m.handleConflictsKey(key)
	case modeConflictDetail:
		return m.handleConflictDetailKey(key)
	case modeHelp:
		if key == "q" {
			m.mode = modeWork
		}
		return m, nil
	}
	return m, nil
}

func (m model) handleSetupKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "c":
		m.mode = modeCreateSecret
		m.inputs = []textinput.Model{newSecretInput("Recovery secret")}
		m.focus = 0
	case "r":
		m.restoreOp = true
		m.mode = modeSyncSetup
		m.inputs = syncSetupInputs(m.config)
		m.focus = 0
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m model) handleLockedKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "u":
		m.mode = modeUnlock
		m.inputs = []textinput.Model{newSecretInput("Recovery secret")}
		m.focus = 0
	case "r":
		m.restoreOp = true
		m.mode = modeSyncSetup
		m.inputs = syncSetupInputs(m.config)
		m.focus = 0
	case "s":
		m.notice = syncHealthText(m.status, m.config)
	case "g":
		m.restoreOp = false
		m.mode = modeSyncSetup
		m.inputs = syncSetupInputs(m.config)
		m.focus = 0
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeSyncSetup {
		switch msg.String() {
		case "ctrl+f":
			return m.openFilePicker(pickerCredentials)
		case "ctrl+d":
			return m.openFilePicker(pickerSyncDir)
		}
	}
	switch msg.String() {
	case "enter":
		switch m.mode {
		case modeUnlock:
			secret := m.inputs[0].Value()
			if strings.TrimSpace(secret) == "" {
				m.err = "Recovery secret is required"
				return m, nil
			}
			m.busy = "Unlocking..."
			return m, m.unlock(secret)
		case modeCreateSecret:
			m.secret = m.inputs[0].Value()
			if m.secret == "" {
				m.err = "Recovery secret is required"
				return m, nil
			}
			m.mode = modeCreateConfirm
			m.inputs = []textinput.Model{newSecretInput("Confirm recovery secret")}
			m.focus = 0
			return m, nil
		case modeCreateConfirm:
			if m.inputs[0].Value() != m.secret {
				m.err = "Recovery Secret confirmation did not match"
				m.mode = modeCreateSecret
				m.inputs = []textinput.Model{newSecretInput("Recovery secret")}
				m.focus = 0
				return m, nil
			}
			m.busy = "Creating Task Collection..."
			return m, m.create(m.secret)
		case modeSearch:
			m.searchQuery = strings.TrimSpace(m.inputs[0].Value())
			if m.searchQuery == "" {
				m.filter = filterWorking
			} else {
				m.filter = filterSearch
			}
			m.mode = modeWork
			m.selected = 0
			return m, nil
		case modeSyncSetup:
			cfg := syncsetup.Config{
				Kind:            syncsetup.Kind(strings.TrimSpace(m.inputs[0].Value())),
				CredentialsPath: strings.TrimSpace(m.inputs[1].Value()),
				Dir:             strings.TrimSpace(m.inputs[2].Value()),
			}
			if cfg.Kind != syncsetup.Google && cfg.Kind != syncsetup.Dir {
				m.err = "sync type must be google or dir"
				return m, nil
			}
			if cfg.Kind == syncsetup.Google && cfg.CredentialsPath == "" {
				m.err = "Google credentials path is required"
				return m, nil
			}
			if cfg.Kind == syncsetup.Dir && cfg.Dir == "" {
				m.err = "sync directory is required"
				return m, nil
			}
			m.config = cfg
			if err := m.service.SaveSyncSetup(cfg); err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.notice = "Local Sync Setup saved"
			if m.restoreOp {
				m.mode = modeRestoreSecret
				m.inputs = []textinput.Model{newSecretInput("Recovery secret")}
				m.focus = 0
				return m, nil
			}
			if m.unlocked {
				m.mode = modeWork
			} else if m.status.Initialized {
				m.mode = modeLocked
			} else {
				m.mode = modeSetup
			}
			return m, nil
		case modeRestoreSecret:
			m.secret = m.inputs[0].Value()
			if m.secret == "" {
				m.err = "Recovery secret is required"
				return m, nil
			}
			m.mode = modeRestoreConfirm
			phrase := restorePhrase(m.status)
			input := newTextInput("Type " + phrase + " to restore")
			m.inputs = []textinput.Model{input}
			m.focus = 0
			return m, nil
		case modeRestoreConfirm:
			if m.inputs[0].Value() != restorePhrase(m.status) {
				m.err = "Restore confirmation did not match"
				return m, nil
			}
			m.busy = "Restoring Task Collection..."
			return m, m.restore()
		}
	case "tab", "shift+tab", "up", "down":
		if len(m.inputs) > 1 {
			m.moveFocus(msg.String())
		}
	}
	return m.updateFocusedInput(msg)
}

func (m model) handleFilePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.mode = modeSyncSetup
		m.pickFor = pickerNone
		return m, nil
	case "p":
		if m.pickFor == pickerSyncDir {
			m.inputs[2].SetValue(m.picker.CurrentDirectory)
			m.mode = modeSyncSetup
			m.pickFor = pickerNone
			m.notice = "Local sync directory selected"
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.picker, cmd = m.picker.Update(msg)
	if didSelect, path := m.picker.DidSelectFile(msg); didSelect {
		switch m.pickFor {
		case pickerCredentials:
			m.inputs[1].SetValue(path)
			m.notice = "Google credentials file selected"
		case pickerSyncDir:
			m.inputs[2].SetValue(path)
			m.notice = "Local sync directory selected"
		}
		m.mode = modeSyncSetup
		m.pickFor = pickerNone
		return m, nil
	}
	if disabled, path := m.picker.DidSelectDisabledFile(msg); disabled {
		m.err = fmt.Sprintf("select a .json credentials file: %s", path)
	}
	return m, cmd
}

func (m model) handleWorkKey(key string) (tea.Model, tea.Cmd) {
	visible := m.visibleTasks()
	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected < len(visible)-1 {
			m.selected++
		}
	case "a":
		m.mode = modeForm
		m.formID = ""
		m.inputs = taskFormInputs(storage.Task{})
		m.focus = 0
	case "e":
		if task, ok := selectedTask(visible, m.selected); ok {
			m.mode = modeForm
			m.formID = task.ID
			m.inputs = taskFormInputs(task)
			m.focus = 0
		}
	case "enter", "v":
		if _, ok := selectedTask(visible, m.selected); ok {
			m.mode = modeDetail
		}
	case "d":
		if _, ok := selectedTask(visible, m.selected); ok {
			m.mode = modeDeleteConfirm
		}
	case " ":
		if task, ok := selectedTask(visible, m.selected); ok {
			status := "done"
			if task.Status == "done" {
				status = "open"
			}
			m.busy = "Updating task..."
			return m, m.setStatus(task.ID, status)
		}
	case "/":
		m.mode = modeSearch
		input := newTextInput("Search")
		input.SetValue(m.searchQuery)
		m.inputs = []textinput.Model{input}
		m.focus = 0
	case "1":
		m.filter = filterWorking
		m.selected = 0
	case "2":
		m.filter = filterAll
		m.selected = 0
	case "3":
		m.filter = filterDone
		m.selected = 0
	case "c":
		m.busy = "Loading conflicts..."
		return m, m.loadConflicts()
	case "s":
		return m.startSync()
	case "S":
		m.restoreOp = false
		m.mode = modeSyncSetup
		m.inputs = syncSetupInputs(m.config)
		m.focus = 0
	case "r":
		m.restoreOp = true
		m.mode = modeSyncSetup
		m.inputs = syncSetupInputs(m.config)
		m.focus = 0
	case "g":
		return m.startGoogleLogin()
	}
	return m, nil
}

func (m model) handleFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "shift+tab", "up", "down":
		m.moveFocus(msg.String())
		return m, nil
	case "enter":
		if m.focus < len(m.inputs)-1 {
			m.focus++
			m.focusInput()
			return m, nil
		}
		return m.saveForm()
	}
	return m.updateFocusedInput(msg)
}

func (m model) handleDetailKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "e":
		if task, ok := selectedTask(m.visibleTasks(), m.selected); ok {
			m.mode = modeForm
			m.formID = task.ID
			m.inputs = taskFormInputs(task)
			m.focus = 0
		}
	case "d":
		m.mode = modeDeleteConfirm
	case "q", "esc":
		m.mode = modeWork
	}
	return m, nil
}

func (m model) handleDeleteKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "y":
		if task, ok := selectedTask(m.visibleTasks(), m.selected); ok {
			m.busy = "Deleting task..."
			return m, m.deleteTask(task.ID)
		}
	case "n", "esc":
		m.mode = modeWork
	}
	return m, nil
}

func (m model) handleConflictsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.conflictSel > 0 {
			m.conflictSel--
		}
	case "down", "j":
		if m.conflictSel < len(m.conflicts)-1 {
			m.conflictSel++
		}
	case "enter", "v":
		if len(m.conflicts) > 0 {
			m.mode = modeConflictDetail
		}
	case "q", "esc":
		m.mode = modeWork
	}
	return m, nil
}

func (m model) handleConflictDetailKey(key string) (tea.Model, tea.Cmd) {
	if len(m.conflicts) == 0 {
		m.mode = modeWork
		return m, nil
	}
	conflict := m.conflicts[m.conflictSel]
	switch key {
	case "l":
		m.busy = "Resolving conflict..."
		return m, m.resolveConflict(conflict.ID, "local")
	case "r":
		m.busy = "Resolving conflict..."
		return m, m.resolveConflict(conflict.ID, "remote")
	case "d":
		if conflict.Type == "duplicate_device_sequence" {
			m.busy = "Resolving conflict..."
			return m, m.resolveConflict(conflict.ID, "")
		}
	case "q", "esc":
		m.mode = modeConflicts
	}
	return m, nil
}

func (m model) saveForm() (tea.Model, tea.Cmd) {
	title := strings.TrimSpace(m.inputs[0].Value())
	if title == "" {
		m.err = "title is required"
		return m, nil
	}
	dueAt, err := parseOptionalTime(m.inputs[3].Value())
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	reminderAt, err := parseOptionalTime(m.inputs[4].Value())
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	tags := splitTags(m.inputs[2].Value())
	input := app.TaskInput{
		Title:      title,
		Body:       m.inputs[1].Value(),
		Tags:       tags,
		DueAt:      dueAt,
		ReminderAt: reminderAt,
	}
	m.busy = "Saving task..."
	return m, m.saveTask(m.formID, input)
}

func (m model) updateFocusedInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.inputs) == 0 {
		return m, nil
	}
	if m.focus < 0 || m.focus >= len(m.inputs) {
		m.focus = 0
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m *model) moveFocus(key string) {
	if len(m.inputs) == 0 {
		return
	}
	switch key {
	case "shift+tab", "up":
		m.focus--
	default:
		m.focus++
	}
	if m.focus < 0 {
		m.focus = len(m.inputs) - 1
	}
	if m.focus >= len(m.inputs) {
		m.focus = 0
	}
	m.focusInput()
}

func (m *model) focusInput() {
	for i := range m.inputs {
		if i == m.focus {
			m.inputs[i].Focus()
		} else {
			m.inputs[i].Blur()
		}
	}
}
