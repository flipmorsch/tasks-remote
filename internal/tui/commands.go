package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"tasks-remote/internal/app"
	"tasks-remote/internal/syncsetup"
)

func (m model) load() tea.Cmd {
	return func() tea.Msg {
		startup, err := m.service.Startup(m.ctx)
		if err != nil {
			return loadedMsg{mode: modeSetup, config: startup.SyncSetup, err: err}
		}
		switch startup.State {
		case app.StateSetup:
			return loadedMsg{mode: modeSetup, status: startup.Status, config: startup.SyncSetup}
		case app.StateLocked:
			return loadedMsg{mode: modeLocked, status: startup.Status, config: startup.SyncSetup}
		default:
			return loadedMsg{mode: modeWork, tasks: startup.Tasks, status: startup.Status, config: startup.SyncSetup}
		}
	}
}

func (m model) unlock(secret string) tea.Cmd {
	return func() tea.Msg {
		tasks, status, err := m.service.Unlock(m.ctx, secret)
		if err != nil {
			return opMsg{err: err}
		}
		return loadedMsg{mode: modeWork, tasks: tasks, status: status, config: m.config}
	}
}

func (m model) create(secret string) tea.Cmd {
	return func() tea.Msg {
		status, err := m.service.CreateTaskCollection(m.ctx, secret)
		if err != nil {
			return opMsg{err: err}
		}
		return loadedMsg{mode: modeWork, status: status, config: m.config}
	}
}

func (m model) refresh() tea.Cmd {
	return func() tea.Msg {
		tasks, status, err := m.service.ListTasks(m.ctx)
		if err != nil {
			return tasksMsg{err: err}
		}
		return tasksMsg{tasks: tasks, status: status}
	}
}

func (m model) saveTask(id string, input app.TaskInput) tea.Cmd {
	return func() tea.Msg {
		if id == "" {
			_, err := m.service.CreateTask(m.ctx, input)
			if err != nil {
				return opMsg{err: err}
			}
		} else {
			_, err := m.service.UpdateTask(m.ctx, id, input)
			if err != nil {
				return opMsg{err: err}
			}
		}
		return opMsg{notice: "Task saved"}
	}
}

func (m model) setStatus(id, status string) tea.Cmd {
	return func() tea.Msg {
		if status == "done" {
			if _, err := m.service.CompleteTask(m.ctx, id); err != nil {
				return opMsg{err: err}
			}
		} else {
			if _, err := m.service.ReopenTask(m.ctx, id); err != nil {
				return opMsg{err: err}
			}
		}
		return opMsg{notice: "Task updated"}
	}
}

func (m model) deleteTask(id string) tea.Cmd {
	return func() tea.Msg {
		if err := m.service.DeleteTask(m.ctx, id); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Task deleted"}
	}
}

func (m model) loadConflicts() tea.Cmd {
	return func() tea.Msg {
		conflicts, err := m.service.ListConflicts(m.ctx)
		return conflictsMsg{conflicts: conflicts, err: err}
	}
}

func (m model) resolveConflict(id, use string) tea.Cmd {
	return func() tea.Msg {
		if err := m.service.ResolveConflict(m.ctx, id, use); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Conflict resolved"}
	}
}

func (m model) startSync() (tea.Model, tea.Cmd) {
	if m.config.Kind == syncsetup.None {
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
		if err := m.service.SyncNow(m.ctx, m.config); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Sync complete"}
	}
}

func (m model) startGoogleLogin() (tea.Model, tea.Cmd) {
	if m.config.Kind != syncsetup.Google || m.config.CredentialsPath == "" {
		m.mode = modeSyncSetup
		m.inputs = syncSetupInputs(m.config)
		m.focus = 0
		m.notice = "Save Google credentials before login"
		return m, nil
	}
	m.busy = "Opening browser for Google login. Return here after authorization..."
	return m, func() tea.Msg {
		if err := m.service.LoginGoogle(m.ctx, m.config); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Google login complete"}
	}
}

func (m model) restore() tea.Cmd {
	return func() tea.Msg {
		if err := m.service.Restore(m.ctx, m.config, m.secret, m.status.Initialized); err != nil {
			return opMsg{err: err}
		}
		return opMsg{notice: "Task Collection restored"}
	}
}
