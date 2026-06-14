package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"tasks-remote/internal/cloudsync"
	"tasks-remote/internal/googleauth"
	"tasks-remote/internal/storage"
	"tasks-remote/internal/unlock"
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

type syncKind string

const (
	syncNone   syncKind = ""
	syncGoogle syncKind = "google"
	syncDir    syncKind = "dir"
)

type syncConfig struct {
	Kind            syncKind `json:"kind"`
	CredentialsPath string   `json:"credentials_path,omitempty"`
	Dir             string   `json:"dir,omitempty"`
}

type configFile struct {
	ByDatabase map[string]syncConfig `json:"by_database"`
}

type model struct {
	ctx    context.Context
	dbPath string
	mode   mode

	width  int
	height int

	store     *storage.Store
	tasks     []storage.Task
	conflicts []storage.ConflictDetail
	status    storage.SyncStatus
	config    syncConfig
	unlocked  bool

	filter      filterMode
	selected    int
	conflictSel int
	err         string
	notice      string
	busy        string
	searchQuery string

	inputs    []textinput.Model
	focus     int
	formID    string
	secret    string
	restoreOp bool
	pendingOp string
}

type loadedMsg struct {
	mode   mode
	tasks  []storage.Task
	status storage.SyncStatus
	config syncConfig
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
		ctx:    ctx,
		dbPath: dbPath,
		mode:   modeLoading,
		filter: filterWorking,
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
		case modeHelp, modeDetail, modeSearch, modeSyncSetup, modeConflicts, modeConflictDetail:
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
			return m, nil
		case modeCreateConfirm:
			if m.inputs[0].Value() != m.secret {
				m.err = "Recovery Secret confirmation did not match"
				m.mode = modeCreateSecret
				m.inputs = []textinput.Model{newSecretInput("Recovery secret")}
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
			cfg := syncConfig{
				Kind:            syncKind(strings.TrimSpace(m.inputs[0].Value())),
				CredentialsPath: strings.TrimSpace(m.inputs[1].Value()),
				Dir:             strings.TrimSpace(m.inputs[2].Value()),
			}
			if cfg.Kind != syncGoogle && cfg.Kind != syncDir {
				m.err = "sync type must be google or dir"
				return m, nil
			}
			if cfg.Kind == syncGoogle && cfg.CredentialsPath == "" {
				m.err = "Google credentials path is required"
				return m, nil
			}
			if cfg.Kind == syncDir && cfg.Dir == "" {
				m.err = "sync directory is required"
				return m, nil
			}
			m.config = cfg
			if err := saveSyncConfig(m.dbPath, cfg); err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.notice = "Local Sync Setup saved"
			if m.restoreOp {
				m.mode = modeRestoreSecret
				m.inputs = []textinput.Model{newSecretInput("Recovery secret")}
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
	input := storage.TaskInput{
		Title:      title,
		Body:       m.inputs[1].Value(),
		DueAt:      dueAt,
		ReminderAt: reminderAt,
	}
	tags := splitTags(m.inputs[2].Value())
	m.busy = "Saving task..."
	return m, m.saveTask(m.formID, input, tags)
}

func (m model) updateFocusedInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.inputs) == 0 {
		return m, nil
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

func (m model) load() tea.Cmd {
	return func() tea.Msg {
		cfg, _ := loadSyncConfig(m.dbPath)
		status, err := storage.ReadSyncStatus(m.ctx, m.dbPath)
		if err != nil {
			return loadedMsg{mode: modeSetup, config: cfg, err: err}
		}
		if !status.Initialized {
			return loadedMsg{mode: modeSetup, status: status, config: cfg}
		}
		secret, err := unlock.Load(m.dbPath)
		if err != nil {
			return loadedMsg{mode: modeLocked, status: status, config: cfg}
		}
		store, err := storage.Open(m.ctx, m.dbPath, secret)
		if err != nil {
			return loadedMsg{mode: modeLocked, status: status, config: cfg, err: err}
		}
		defer store.Close()
		tasks, err := store.ListTasks(m.ctx)
		if err != nil {
			return loadedMsg{mode: modeLocked, status: status, config: cfg, err: err}
		}
		return loadedMsg{mode: modeWork, tasks: tasks, status: status, config: cfg}
	}
}

func (m model) unlock(secret string) tea.Cmd {
	return func() tea.Msg {
		store, err := storage.Open(m.ctx, m.dbPath, secret)
		if err != nil {
			return opMsg{err: err}
		}
		defer store.Close()
		if err := unlock.Save(m.dbPath, secret); err != nil {
			return opMsg{err: err}
		}
		tasks, err := store.ListTasks(m.ctx)
		if err != nil {
			return opMsg{err: err}
		}
		status, err := storage.ReadSyncStatus(m.ctx, m.dbPath)
		if err != nil {
			return opMsg{err: err}
		}
		return loadedMsg{mode: modeWork, tasks: tasks, status: status, config: m.config}
	}
}

func (m model) create(secret string) tea.Cmd {
	return func() tea.Msg {
		if err := storage.Init(m.ctx, m.dbPath, secret); err != nil {
			return opMsg{err: err}
		}
		if err := unlock.Save(m.dbPath, secret); err != nil {
			return opMsg{err: err}
		}
		status, err := storage.ReadSyncStatus(m.ctx, m.dbPath)
		if err != nil {
			return opMsg{err: err}
		}
		return loadedMsg{mode: modeWork, status: status, config: m.config}
	}
}

func (m model) refresh() tea.Cmd {
	return func() tea.Msg {
		secret, err := unlock.Load(m.dbPath)
		if err != nil {
			return tasksMsg{err: err}
		}
		store, err := storage.Open(m.ctx, m.dbPath, secret)
		if err != nil {
			return tasksMsg{err: err}
		}
		defer store.Close()
		tasks, err := store.ListTasks(m.ctx)
		if err != nil {
			return tasksMsg{err: err}
		}
		status, err := storage.ReadSyncStatus(m.ctx, m.dbPath)
		if err != nil {
			return tasksMsg{err: err}
		}
		return tasksMsg{tasks: tasks, status: status}
	}
}

func (m model) saveTask(id string, input storage.TaskInput, tags []string) tea.Cmd {
	return func() tea.Msg {
		secret, err := unlock.Load(m.dbPath)
		if err != nil {
			return opMsg{err: err}
		}
		store, err := storage.Open(m.ctx, m.dbPath, secret)
		if err != nil {
			return opMsg{err: err}
		}
		defer store.Close()
		var task storage.Task
		if id == "" {
			task, err = store.AddTaskWithInput(m.ctx, input)
		} else {
			task, err = store.EditTaskWithInput(m.ctx, id, input)
		}
		if err != nil {
			return opMsg{err: err}
		}
		if err := reconcileTags(m.ctx, store, task, tags); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Task saved"}
	}
}

func (m model) setStatus(id, status string) tea.Cmd {
	return func() tea.Msg {
		secret, err := unlock.Load(m.dbPath)
		if err != nil {
			return opMsg{err: err}
		}
		store, err := storage.Open(m.ctx, m.dbPath, secret)
		if err != nil {
			return opMsg{err: err}
		}
		defer store.Close()
		if _, err := store.SetTaskStatus(m.ctx, id, status); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Task updated"}
	}
}

func (m model) deleteTask(id string) tea.Cmd {
	return func() tea.Msg {
		secret, err := unlock.Load(m.dbPath)
		if err != nil {
			return opMsg{err: err}
		}
		store, err := storage.Open(m.ctx, m.dbPath, secret)
		if err != nil {
			return opMsg{err: err}
		}
		defer store.Close()
		if err := store.DeleteTask(m.ctx, id); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Task deleted"}
	}
}

func (m model) loadConflicts() tea.Cmd {
	return func() tea.Msg {
		secret, err := unlock.Load(m.dbPath)
		if err != nil {
			return conflictsMsg{err: err}
		}
		store, err := storage.Open(m.ctx, m.dbPath, secret)
		if err != nil {
			return conflictsMsg{err: err}
		}
		defer store.Close()
		conflicts, err := store.ListConflictDetails(m.ctx)
		return conflictsMsg{conflicts: conflicts, err: err}
	}
}

func (m model) resolveConflict(id, use string) tea.Cmd {
	return func() tea.Msg {
		secret, err := unlock.Load(m.dbPath)
		if err != nil {
			return opMsg{err: err}
		}
		store, err := storage.Open(m.ctx, m.dbPath, secret)
		if err != nil {
			return opMsg{err: err}
		}
		defer store.Close()
		if err := store.ResolveConflict(m.ctx, id, use); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Conflict resolved"}
	}
}

func (m model) startSync() (tea.Model, tea.Cmd) {
	if m.config.Kind == syncNone {
		m.mode = modeSyncSetup
		m.inputs = syncSetupInputs(m.config)
		m.focus = 0
		m.notice = "Configure Local Sync Setup before syncing"
		return m, nil
	}
	m.busy = "Syncing..."
	return m, m.syncNow()
}

func (m model) syncNow() tea.Cmd {
	return func() tea.Msg {
		client, err := m.syncClient()
		if err != nil {
			return opMsg{err: err}
		}
		secret, err := unlock.Load(m.dbPath)
		if err != nil {
			return opMsg{err: err}
		}
		store, err := storage.Open(m.ctx, m.dbPath, secret)
		if err != nil {
			return opMsg{err: err}
		}
		defer store.Close()
		if err := cloudsync.Push(m.ctx, store, client); err != nil {
			return opMsg{err: err}
		}
		if err := cloudsync.Pull(m.ctx, store, client); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Sync complete"}
	}
}

func (m model) startGoogleLogin() (tea.Model, tea.Cmd) {
	if m.config.Kind != syncGoogle || m.config.CredentialsPath == "" {
		m.mode = modeSyncSetup
		m.inputs = syncSetupInputs(m.config)
		m.focus = 0
		m.notice = "Save Google credentials before login"
		return m, nil
	}
	m.busy = "Opening browser for Google login. Return here after authorization..."
	return m, func() tea.Msg {
		config, err := googleauth.ConfigFromCredentialsFile(m.config.CredentialsPath)
		if err != nil {
			return opMsg{err: err}
		}
		if err := googleauth.Login(m.ctx, config); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Google login complete"}
	}
}

func (m model) restore() tea.Cmd {
	return func() tea.Msg {
		client, err := m.syncClient()
		if err != nil {
			return opMsg{err: err}
		}
		manifest, err := cloudsync.ReadManifest(m.ctx, client)
		if err != nil {
			return opMsg{err: err}
		}
		if m.status.Initialized {
			for _, path := range []string{m.dbPath, m.dbPath + "-wal", m.dbPath + "-shm"} {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return opMsg{err: fmt.Errorf("replace local database: %w", err)}
				}
			}
		}
		if err := storage.InitWithManifest(m.ctx, m.dbPath, m.secret, manifest); err != nil {
			return opMsg{err: err}
		}
		store, err := storage.Open(m.ctx, m.dbPath, m.secret)
		if err != nil {
			return opMsg{err: err}
		}
		if err := cloudsync.Pull(m.ctx, store, client); err != nil {
			store.Close()
			return opMsg{err: err}
		}
		if err := store.Close(); err != nil {
			return opMsg{err: err}
		}
		if err := unlock.Save(m.dbPath, m.secret); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Task Collection restored"}
	}
}

func (m model) syncClient() (cloudsync.Client, error) {
	switch m.config.Kind {
	case syncDir:
		return cloudsync.LocalDirClient{Dir: m.config.Dir}, nil
	case syncGoogle:
		config, err := googleauth.ConfigFromCredentialsFile(m.config.CredentialsPath)
		if err != nil {
			return nil, err
		}
		source, err := googleauth.TokenSource(m.ctx, config)
		if err != nil {
			return nil, err
		}
		service, err := drive.NewService(m.ctx, option.WithTokenSource(source))
		if err != nil {
			return nil, fmt.Errorf("create google drive service: %w", err)
		}
		return cloudsync.GoogleDriveClient{Service: service}, nil
	default:
		return nil, fmt.Errorf("sync is not configured")
	}
}

func (m model) View() string {
	var b strings.Builder
	title := "tasks-remote"
	if m.width > 0 && m.width < 56 {
		title = "tasks"
	}
	b.WriteString(headerStyle().Render(title))
	b.WriteString("\n")
	if m.busy != "" {
		b.WriteString(m.busy)
		b.WriteString("\n")
		return b.String()
	}
	if m.err != "" {
		b.WriteString(errorStyle().Render(m.err))
		b.WriteString("\n")
	}
	if m.notice != "" {
		b.WriteString(noticeStyle().Render(m.notice))
		b.WriteString("\n")
	}
	switch m.mode {
	case modeLoading:
		b.WriteString("Loading...\n")
	case modeSetup:
		b.WriteString("No local Task Collection is available for this database.\n\n")
		b.WriteString("[c] Create New Task Collection\n")
		b.WriteString("[r] Restore Existing Task Collection\n")
		b.WriteString("[q] Quit\n")
	case modeLocked:
		b.WriteString("Locked Device. Sensitive Task Data is hidden until unlock.\n\n")
		b.WriteString(fmt.Sprintf("Sync Health: %s\n\n", syncHealthText(m.status, m.config)))
		b.WriteString("[u] Unlock  [r] Restore  [s] Sync Health  [g] Sync Setup/Login  [q] Quit\n")
	case modeUnlock, modeCreateSecret, modeCreateConfirm, modeSearch, modeRestoreSecret, modeRestoreConfirm:
		b.WriteString(renderInputs(m.inputs, m.focus))
		b.WriteString("\nenter: continue  esc: cancel\n")
	case modeSyncSetup:
		b.WriteString("Local Sync Setup\n")
		b.WriteString("Type: google or dir. Google uses Drive app data; dir is for local testing.\n\n")
		b.WriteString(renderInputs(m.inputs, m.focus))
		b.WriteString("\nenter: save  tab: next  esc: cancel\n")
	case modeWork:
		b.WriteString(m.renderWork())
	case modeForm:
		if m.formID == "" {
			b.WriteString("Create Task\n\n")
		} else {
			b.WriteString("Edit Task\n\n")
		}
		b.WriteString(renderInputs(m.inputs, m.focus))
		b.WriteString("\nenter: next/save  tab: next  esc: cancel\n")
	case modeDetail:
		b.WriteString(m.renderDetail())
	case modeDeleteConfirm:
		if task, ok := selectedTask(m.visibleTasks(), m.selected); ok {
			b.WriteString(fmt.Sprintf("Delete task %q?\n\n[y] Delete  [n] Cancel\n", task.Title))
		}
	case modeConflicts:
		b.WriteString(m.renderConflicts())
	case modeConflictDetail:
		b.WriteString(m.renderConflictDetail())
	case modeHelp:
		b.WriteString(helpText())
	}
	return b.String()
}

func (m model) renderWork() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Sync Health: %s\n", syncHealthText(m.status, m.config)))
	b.WriteString(fmt.Sprintf("View: %s\n\n", filterLabel(m.filter)))
	tasks := m.visibleTasks()
	if len(tasks) == 0 {
		b.WriteString("No tasks in this view.\n")
	} else {
		for i, task := range tasks {
			prefix := "  "
			if i == m.selected {
				prefix = "> "
			}
			line := fmt.Sprintf("%s[%s] %s%s%s", prefix, task.Status, task.Title, formatTags(task.Tags), formatDates(task))
			b.WriteString(truncate(line, maxWidth(m.width)))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	if m.width > 0 && m.width < 70 {
		b.WriteString("[?] help  [q] quit\n")
	} else {
		b.WriteString("[a] add  [e] edit  [space] done/reopen  [d] delete  [/] search  [s] Sync Now  [?] help  [q] quit\n")
		b.WriteString("[1] Working  [2] All  [3] Done  [c] Conflicts  [S] Sync Setup  [g] Google Login  [r] Restore\n")
	}
	return b.String()
}

func (m model) renderDetail() string {
	task, ok := selectedTask(m.visibleTasks(), m.selected)
	if !ok {
		return "No task selected.\n"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s [%s]\n", task.Title, task.Status))
	b.WriteString(fmt.Sprintf("id: %s\n", task.ID))
	if len(task.Tags) > 0 {
		b.WriteString(fmt.Sprintf("tags: %s\n", strings.Join(task.Tags, ", ")))
	}
	if task.DueAt != nil {
		b.WriteString(fmt.Sprintf("due: %s\n", task.DueAt.Format(time.RFC3339)))
	}
	if task.ReminderAt != nil {
		b.WriteString(fmt.Sprintf("reminder: %s\n", task.ReminderAt.Format(time.RFC3339)))
	}
	if task.Body != "" {
		b.WriteString("\n")
		b.WriteString(task.Body)
		b.WriteString("\n")
	}
	b.WriteString("\n[e] edit  [d] delete  [esc] back\n")
	return b.String()
}

func (m model) renderConflicts() string {
	var b strings.Builder
	b.WriteString("Sync Conflicts\n\n")
	if len(m.conflicts) == 0 {
		b.WriteString("No open conflicts.\n")
	} else {
		for i, conflict := range m.conflicts {
			prefix := "  "
			if i == m.conflictSel {
				prefix = "> "
			}
			b.WriteString(fmt.Sprintf("%s%s %s task=%s\n", prefix, conflict.ID, conflict.Type, conflict.TaskID))
		}
	}
	b.WriteString("\n[enter] view  [esc] back\n")
	return b.String()
}

func (m model) renderConflictDetail() string {
	if len(m.conflicts) == 0 {
		return "No conflict selected.\n"
	}
	conflict := m.conflicts[m.conflictSel]
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %s task=%s\n\n", conflict.ID, conflict.Type, conflict.TaskID))
	b.WriteString("local:  " + formatConflictSide(conflict.Local) + "\n")
	b.WriteString("remote: " + formatConflictSide(conflict.Remote) + "\n\n")
	if conflict.Type == "duplicate_device_sequence" {
		b.WriteString("[d] dismiss duplicate  [esc] back\n")
	} else {
		b.WriteString("[l] use local  [r] use remote  [esc] back\n")
	}
	return b.String()
}

func (m model) visibleTasks() []storage.Task {
	tasks := append([]storage.Task(nil), m.tasks...)
	sort.SliceStable(tasks, func(i, j int) bool {
		return taskRank(tasks[i]) < taskRank(tasks[j])
	})
	filtered := tasks[:0]
	query := strings.ToLower(strings.TrimSpace(m.searchQuery))
	now := time.Now()
	for _, task := range tasks {
		switch m.filter {
		case filterWorking:
			if task.Status != "done" && isWorkingTask(task, now) {
				filtered = append(filtered, task)
			}
		case filterAll:
			filtered = append(filtered, task)
		case filterDone:
			if task.Status == "done" {
				filtered = append(filtered, task)
			}
		case filterSearch:
			if query == "" || taskMatches(task, query) {
				filtered = append(filtered, task)
			}
		}
	}
	return filtered
}

func isWorkingTask(task storage.Task, now time.Time) bool {
	if task.Status == "done" {
		return false
	}
	if task.DueAt == nil && task.ReminderAt == nil {
		return true
	}
	horizon := now.Add(7 * 24 * time.Hour)
	return (task.DueAt != nil && task.DueAt.Before(horizon)) || (task.ReminderAt != nil && task.ReminderAt.Before(horizon))
}

func taskRank(task storage.Task) int64 {
	var t time.Time
	switch {
	case task.ReminderAt != nil:
		t = *task.ReminderAt
	case task.DueAt != nil:
		t = *task.DueAt
	default:
		t = task.CreatedAt
	}
	return t.Unix()
}

func taskMatches(task storage.Task, query string) bool {
	return strings.Contains(strings.ToLower(task.Title), query) ||
		strings.Contains(strings.ToLower(task.Body), query) ||
		tagsContain(task.Tags, query)
}

func (m *model) clampSelection() {
	tasks := m.visibleTasks()
	if m.selected >= len(tasks) {
		m.selected = max(0, len(tasks)-1)
	}
}

func selectedTask(tasks []storage.Task, selected int) (storage.Task, bool) {
	if selected < 0 || selected >= len(tasks) {
		return storage.Task{}, false
	}
	return tasks[selected], true
}

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

func syncSetupInputs(cfg syncConfig) []textinput.Model {
	kind := newTextInput("google or dir")
	if cfg.Kind != syncNone {
		kind.SetValue(string(cfg.Kind))
	} else {
		kind.SetValue(string(syncGoogle))
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

func reconcileTags(ctx context.Context, store *storage.Store, task storage.Task, desired []string) error {
	current := map[string]bool{}
	for _, tag := range task.Tags {
		current[tag] = true
	}
	next := map[string]bool{}
	for _, tag := range desired {
		next[tag] = true
		if !current[tag] {
			updated, err := store.AddTag(ctx, task.ID, tag)
			if err != nil {
				return err
			}
			task = updated
		}
	}
	for _, tag := range task.Tags {
		if !next[tag] {
			updated, err := store.RemoveTag(ctx, task.ID, tag)
			if err != nil {
				return err
			}
			task = updated
		}
	}
	return nil
}

func splitTags(raw string) []string {
	seen := map[string]bool{}
	var tags []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		tag := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(part, "#")))
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func parseOptionalTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		value := parsed.UTC().Truncate(time.Second)
		return &value, nil
	}
	if parsed, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
		value := parsed.UTC().Truncate(time.Second)
		return &value, nil
	}
	return nil, fmt.Errorf("invalid time %q: use YYYY-MM-DD or RFC3339", raw)
}

func syncHealthText(status storage.SyncStatus, cfg syncConfig) string {
	if !status.Initialized {
		return "not initialized"
	}
	if cfg.Kind == syncNone {
		return "not configured"
	}
	if status.OpenConflicts > 0 {
		return fmt.Sprintf("%d conflict(s) open", status.OpenConflicts)
	}
	if status.PendingChanges > 0 {
		return fmt.Sprintf("%d pending local change(s)", status.PendingChanges)
	}
	return "caught up"
}

func restorePhrase(status storage.SyncStatus) string {
	if status.Initialized && status.PendingChanges > 0 {
		return "RESTORE WITH PENDING"
	}
	return "RESTORE"
}

func filterLabel(filter filterMode) string {
	switch filter {
	case filterAll:
		return "All"
	case filterDone:
		return "Done"
	case filterSearch:
		return "Search"
	default:
		return "Working"
	}
}

func formatTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	formatted := make([]string, 0, len(tags))
	for _, tag := range tags {
		formatted = append(formatted, "#"+tag)
	}
	return " " + strings.Join(formatted, " ")
}

func formatDates(task storage.Task) string {
	var parts []string
	if task.DueAt != nil {
		parts = append(parts, "due:"+task.DueAt.Format("2006-01-02"))
	}
	if task.ReminderAt != nil {
		parts = append(parts, "remind:"+task.ReminderAt.Format("2006-01-02"))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func formatConflictSide(side storage.ConflictSide) string {
	device := side.DeviceID
	if len(device) > 16 {
		device = device[:16]
	}
	if !side.Present {
		return fmt.Sprintf("[change %s] not stored (rejected duplicate)", side.ChangeID)
	}
	if side.Deleted {
		return fmt.Sprintf("[device %s] deleted task", device)
	}
	summary := side.Title
	if side.Body != "" {
		summary += " - " + side.Body
	}
	return fmt.Sprintf("[device %s] %s%s", device, summary, formatTags(side.Tags))
}

func tagsContain(tags []string, query string) bool {
	for _, tag := range tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}

func loadSyncConfig(dbPath string) (syncConfig, error) {
	path, err := syncConfigPath()
	if err != nil {
		return syncConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return syncConfig{}, nil
		}
		return syncConfig{}, err
	}
	var file configFile
	if err := json.Unmarshal(data, &file); err != nil {
		return syncConfig{}, fmt.Errorf("decode Local Sync Setup: %w", err)
	}
	return file.ByDatabase[dbPath], nil
}

func saveSyncConfig(dbPath string, cfg syncConfig) error {
	path, err := syncConfigPath()
	if err != nil {
		return err
	}
	file := configFile{ByDatabase: map[string]syncConfig{}}
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &file)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if file.ByDatabase == nil {
		file.ByDatabase = map[string]syncConfig{}
	}
	file.ByDatabase[dbPath] = cfg
	data, err = json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Local Sync Setup: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func syncConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tasks-remote", "sync.json"), nil
}

func helpText() string {
	return `[a] add task
[e] edit selected task
[space] complete or reopen selected task
[d] delete selected task with confirmation
[/] search title, body, and tags
[1] Working View  [2] All  [3] Done
[s] Sync Now  [S] Local Sync Setup  [g] Google login  [r] Restore
[c] Sync Conflicts
[?] help  [esc] back  [q] quit
`
}

func headerStyle() lipgloss.Style {
	if noColor() {
		return lipgloss.NewStyle().Bold(true)
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
}

func errorStyle() lipgloss.Style {
	if noColor() {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
}

func noticeStyle() lipgloss.Style {
	if noColor() {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
}

func noColor() bool {
	return os.Getenv("NO_COLOR") != ""
}

func truncate(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	if width <= 3 {
		return s[:width]
	}
	return s[:width-3] + "..."
}

func maxWidth(width int) int {
	if width <= 0 {
		return 100
	}
	if width < 24 {
		return width
	}
	return width - 2
}
