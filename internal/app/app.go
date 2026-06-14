package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"tasks-remote/internal/cloudsync"
	"tasks-remote/internal/googleauth"
	"tasks-remote/internal/storage"
	"tasks-remote/internal/syncsetup"
	"tasks-remote/internal/unlock"
)

type Service struct {
	DBPath string
}

type StartupState string

const (
	StateSetup    StartupState = "setup"
	StateLocked   StartupState = "locked"
	StateUnlocked StartupState = "unlocked"
)

type Startup struct {
	State     StartupState
	Tasks     []storage.Task
	Status    storage.SyncStatus
	SyncSetup syncsetup.Config
}

type TaskInput struct {
	Title      string
	Body       string
	Tags       []string
	DueAt      *time.Time
	ReminderAt *time.Time
}

func (s Service) Startup(ctx context.Context) (Startup, error) {
	cfg, err := syncsetup.Load(s.DBPath)
	if err != nil {
		return Startup{State: StateSetup}, err
	}
	status, err := storage.ReadSyncStatus(ctx, s.DBPath)
	if err != nil {
		return Startup{State: StateSetup, SyncSetup: cfg}, err
	}
	if !status.Initialized {
		return Startup{State: StateSetup, Status: status, SyncSetup: cfg}, nil
	}
	secret, err := unlock.Load(s.DBPath)
	if err != nil {
		return Startup{State: StateLocked, Status: status, SyncSetup: cfg}, nil
	}
	tasks, err := s.listTasksWithSecret(ctx, secret)
	if err != nil {
		return Startup{State: StateLocked, Status: status, SyncSetup: cfg}, err
	}
	return Startup{State: StateUnlocked, Tasks: tasks, Status: status, SyncSetup: cfg}, nil
}

func (s Service) CreateTaskCollection(ctx context.Context, recoverySecret string) (storage.SyncStatus, error) {
	if err := storage.Init(ctx, s.DBPath, recoverySecret); err != nil {
		return storage.SyncStatus{}, err
	}
	if err := unlock.Save(s.DBPath, recoverySecret); err != nil {
		return storage.SyncStatus{}, err
	}
	return storage.ReadSyncStatus(ctx, s.DBPath)
}

func (s Service) Unlock(ctx context.Context, recoverySecret string) ([]storage.Task, storage.SyncStatus, error) {
	store, err := storage.Open(ctx, s.DBPath, recoverySecret)
	if err != nil {
		return nil, storage.SyncStatus{}, err
	}
	defer store.Close()
	if err := unlock.Save(s.DBPath, recoverySecret); err != nil {
		return nil, storage.SyncStatus{}, err
	}
	tasks, err := store.ListTasks(ctx)
	if err != nil {
		return nil, storage.SyncStatus{}, err
	}
	status, err := storage.ReadSyncStatus(ctx, s.DBPath)
	if err != nil {
		return nil, storage.SyncStatus{}, err
	}
	return tasks, status, nil
}

func (s Service) ReadSyncHealth(ctx context.Context) (storage.SyncStatus, error) {
	return storage.ReadSyncStatus(ctx, s.DBPath)
}

func (s Service) ListTasks(ctx context.Context) ([]storage.Task, storage.SyncStatus, error) {
	store, err := s.open(ctx)
	if err != nil {
		return nil, storage.SyncStatus{}, err
	}
	defer store.Close()
	tasks, err := store.ListTasks(ctx)
	if err != nil {
		return nil, storage.SyncStatus{}, err
	}
	status, err := storage.ReadSyncStatus(ctx, s.DBPath)
	if err != nil {
		return nil, storage.SyncStatus{}, err
	}
	return tasks, status, nil
}

func (s Service) SearchTasks(ctx context.Context, query string) ([]storage.Task, storage.SyncStatus, error) {
	store, err := s.open(ctx)
	if err != nil {
		return nil, storage.SyncStatus{}, err
	}
	defer store.Close()
	tasks, err := store.SearchTasks(ctx, query)
	if err != nil {
		return nil, storage.SyncStatus{}, err
	}
	status, err := storage.ReadSyncStatus(ctx, s.DBPath)
	if err != nil {
		return nil, storage.SyncStatus{}, err
	}
	return tasks, status, nil
}

func (s Service) CreateTask(ctx context.Context, input TaskInput) (storage.Task, error) {
	store, err := s.open(ctx)
	if err != nil {
		return storage.Task{}, err
	}
	defer store.Close()
	task, err := store.AddTaskWithInput(ctx, storage.TaskInput{
		Title:      input.Title,
		Body:       input.Body,
		DueAt:      input.DueAt,
		ReminderAt: input.ReminderAt,
	})
	if err != nil {
		return storage.Task{}, err
	}
	if err := reconcileTags(ctx, store, task, input.Tags); err != nil {
		return storage.Task{}, err
	}
	return store.GetTask(ctx, task.ID)
}

func (s Service) UpdateTask(ctx context.Context, id string, input TaskInput) (storage.Task, error) {
	store, err := s.open(ctx)
	if err != nil {
		return storage.Task{}, err
	}
	defer store.Close()
	task, err := store.EditTaskWithInput(ctx, id, storage.TaskInput{
		Title:      input.Title,
		Body:       input.Body,
		DueAt:      input.DueAt,
		ReminderAt: input.ReminderAt,
	})
	if err != nil {
		return storage.Task{}, err
	}
	if err := reconcileTags(ctx, store, task, input.Tags); err != nil {
		return storage.Task{}, err
	}
	return store.GetTask(ctx, id)
}

func (s Service) CompleteTask(ctx context.Context, id string) (storage.Task, error) {
	return s.setTaskStatus(ctx, id, "done")
}

func (s Service) ReopenTask(ctx context.Context, id string) (storage.Task, error) {
	return s.setTaskStatus(ctx, id, "open")
}

func (s Service) DeleteTask(ctx context.Context, id string) error {
	store, err := s.open(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.DeleteTask(ctx, id)
}

func (s Service) ListConflicts(ctx context.Context) ([]storage.ConflictDetail, error) {
	store, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.ListConflictDetails(ctx)
}

func (s Service) ResolveConflict(ctx context.Context, id, use string) error {
	store, err := s.open(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.ResolveConflict(ctx, id, use)
}

func (s Service) SaveSyncSetup(cfg syncsetup.Config) error {
	return syncsetup.Save(s.DBPath, cfg)
}

func (s Service) LoadSyncSetup() (syncsetup.Config, error) {
	return syncsetup.Load(s.DBPath)
}

func (s Service) LoginGoogle(ctx context.Context, cfg syncsetup.Config) error {
	if cfg.Kind != syncsetup.Google {
		return fmt.Errorf("Google login requires Google Local Sync Setup")
	}
	config, err := googleauth.ConfigFromCredentialsFile(cfg.CredentialsPath)
	if err != nil {
		return err
	}
	return googleauth.Login(ctx, config)
}

func (s Service) SyncNow(ctx context.Context, cfg syncsetup.Config) error {
	client, err := s.syncClient(ctx, cfg)
	if err != nil {
		return err
	}
	store, err := s.open(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := cloudsync.Push(ctx, store, client); err != nil {
		return err
	}
	return cloudsync.Pull(ctx, store, client)
}

func (s Service) Restore(ctx context.Context, cfg syncsetup.Config, recoverySecret string, replaceExisting bool) error {
	client, err := s.syncClient(ctx, cfg)
	if err != nil {
		return err
	}
	manifest, err := cloudsync.ReadManifest(ctx, client)
	if err != nil {
		return err
	}
	if replaceExisting {
		for _, path := range []string{s.DBPath, s.DBPath + "-wal", s.DBPath + "-shm"} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("replace local database: %w", err)
			}
		}
	}
	if err := storage.InitWithManifest(ctx, s.DBPath, recoverySecret, manifest); err != nil {
		return err
	}
	store, err := storage.Open(ctx, s.DBPath, recoverySecret)
	if err != nil {
		return err
	}
	if err := cloudsync.Pull(ctx, store, client); err != nil {
		store.Close()
		return err
	}
	if err := store.Close(); err != nil {
		return err
	}
	return unlock.Save(s.DBPath, recoverySecret)
}

func (s Service) open(ctx context.Context) (*storage.Store, error) {
	secret, err := unlock.Load(s.DBPath)
	if err != nil {
		return nil, err
	}
	return storage.Open(ctx, s.DBPath, secret)
}

func (s Service) listTasksWithSecret(ctx context.Context, secret string) ([]storage.Task, error) {
	store, err := storage.Open(ctx, s.DBPath, secret)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.ListTasks(ctx)
}

func (s Service) setTaskStatus(ctx context.Context, id, status string) (storage.Task, error) {
	store, err := s.open(ctx)
	if err != nil {
		return storage.Task{}, err
	}
	defer store.Close()
	return store.SetTaskStatus(ctx, id, status)
}

func (s Service) syncClient(ctx context.Context, cfg syncsetup.Config) (cloudsync.Client, error) {
	switch cfg.Kind {
	case syncsetup.Dir:
		return cloudsync.LocalDirClient{Dir: cfg.Dir}, nil
	case syncsetup.Google:
		config, err := googleauth.ConfigFromCredentialsFile(cfg.CredentialsPath)
		if err != nil {
			return nil, err
		}
		source, err := googleauth.TokenSource(ctx, config)
		if err != nil {
			return nil, err
		}
		service, err := drive.NewService(ctx, option.WithTokenSource(source))
		if err != nil {
			return nil, fmt.Errorf("create google drive service: %w", err)
		}
		return cloudsync.GoogleDriveClient{Service: service}, nil
	default:
		return nil, fmt.Errorf("sync is not configured")
	}
}

func reconcileTags(ctx context.Context, store *storage.Store, task storage.Task, desired []string) error {
	desired = normalizeTags(desired)
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

func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(tag, "#")))
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		normalized = append(normalized, tag)
	}
	sort.Strings(normalized)
	return normalized
}
