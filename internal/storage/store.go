package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"tasks-remote/internal/secure"
)

const payloadKeyID = "local-v1"

type Store struct {
	db  *sql.DB
	key []byte
}

type Task struct {
	ID         string
	Title      string
	Body       string
	Tags       []string
	DueAt      *time.Time
	ReminderAt *time.Time
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type taskPayload struct {
	Title      string     `json:"title"`
	Body       string     `json:"body,omitempty"`
	Tags       []string   `json:"tags,omitempty"`
	DueAt      *time.Time `json:"due_at,omitempty"`
	ReminderAt *time.Time `json:"reminder_at,omitempty"`
}

type TaskInput struct {
	Title      string
	Body       string
	DueAt      *time.Time
	ReminderAt *time.Time
}

type Change struct {
	ID        string
	DeviceID  string
	Sequence  int64
	TaskID    string
	Type      string
	CreatedAt time.Time
}

type SyncStatus struct {
	Initialized    bool
	TotalChanges   int
	PendingChanges int
	OpenConflicts  int
	LastChangeAt   *time.Time
}

type SyncConflict struct {
	ID             string
	Type           string
	DeviceID       string
	Sequence       int64
	LocalChangeID  string
	RemoteChangeID string
	CreatedAt      time.Time
}

type Manifest struct {
	Version int    `json:"version"`
	Salt    string `json:"salt"`
}

type ExportedChange struct {
	ChangeID         string `json:"change_id"`
	DeviceID         string `json:"device_id"`
	Sequence         int64  `json:"sequence"`
	TaskID           string `json:"task_id"`
	ChangeType       string `json:"change_type"`
	EncryptedPayload string `json:"encrypted_payload"`
	PayloadNonce     string `json:"payload_nonce"`
	PayloadKeyID     string `json:"payload_key_id"`
	CreatedAt        string `json:"created_at"`
}

type artifactEnvelope struct {
	Version    int    `json:"version"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type changePayload struct {
	Title      string     `json:"title,omitempty"`
	Body       string     `json:"body,omitempty"`
	Tags       []string   `json:"tags,omitempty"`
	DueAt      *time.Time `json:"due_at,omitempty"`
	ReminderAt *time.Time `json:"reminder_at,omitempty"`
	Status     string     `json:"status,omitempty"`
}

func ReadSyncStatus(ctx context.Context, path string) (SyncStatus, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return SyncStatus{Initialized: false}, nil
		}
		return SyncStatus{}, fmt.Errorf("stat database: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return SyncStatus{}, fmt.Errorf("open sqlite: %w", err)
	}
	defer db.Close()
	var status SyncStatus
	status.Initialized = true
	if err := db.QueryRowContext(ctx, `select count(*) from task_changes`).Scan(&status.TotalChanges); err != nil {
		return SyncStatus{}, fmt.Errorf("count task changes: %w", err)
	}
	if err := db.QueryRowContext(ctx, `select count(*) from task_changes where sync_state = 'pending'`).Scan(&status.PendingChanges); err != nil {
		return SyncStatus{}, fmt.Errorf("count pending task changes: %w", err)
	}
	if err := db.QueryRowContext(ctx, `select count(*) from sync_conflicts where resolved_at is null`).Scan(&status.OpenConflicts); err != nil {
		return SyncStatus{}, fmt.Errorf("count sync conflicts: %w", err)
	}
	var last sql.NullString
	if err := db.QueryRowContext(ctx, `select max(created_at) from task_changes`).Scan(&last); err != nil {
		return SyncStatus{}, fmt.Errorf("read last task change: %w", err)
	}
	if last.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, last.String)
		if err != nil {
			return SyncStatus{}, fmt.Errorf("parse last task change: %w", err)
		}
		status.LastChangeAt = &parsed
	}
	return status, nil
}

func Init(ctx context.Context, path string, recoverySecret string) error {
	salt, err := secure.RandomBytes(secure.SaltSize)
	if err != nil {
		return err
	}
	return InitWithSalt(ctx, path, recoverySecret, salt)
}

func InitWithManifest(ctx context.Context, path string, recoverySecret string, manifest Manifest) error {
	if manifest.Version != 1 {
		return fmt.Errorf("unsupported manifest version: %d", manifest.Version)
	}
	salt, err := base64.StdEncoding.DecodeString(manifest.Salt)
	if err != nil {
		return fmt.Errorf("decode manifest salt: %w", err)
	}
	return InitWithSalt(ctx, path, recoverySecret, salt)
}

func InitWithSalt(ctx context.Context, path string, recoverySecret string, salt []byte) error {
	if len(salt) != secure.SaltSize {
		return fmt.Errorf("salt must be %d bytes", secure.SaltSize)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer db.Close()
	if err := migrate(ctx, db); err != nil {
		return err
	}
	var count int
	if err := db.QueryRowContext(ctx, `select count(*) from app_meta where key = 'kdf_salt'`).Scan(&count); err != nil {
		return fmt.Errorf("read kdf metadata: %w", err)
	}
	if count > 0 {
		return nil
	}
	if _, err := secure.DeriveKey(recoverySecret, salt, secure.DefaultKDFParams()); err != nil {
		return err
	}
	deviceID := newID("device")
	_, err = db.ExecContext(ctx, `
		insert into app_meta(key, value) values('kdf_salt', ?), ('device_id', ?)`,
		base64.StdEncoding.EncodeToString(salt), deviceID)
	if err != nil {
		return fmt.Errorf("store kdf metadata: %w", err)
	}
	return nil
}

func Open(ctx context.Context, path string, recoverySecret string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	salt, err := readSalt(ctx, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	key, err := secure.DeriveKey(recoverySecret, salt, secure.DefaultKDFParams())
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, key: key}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Manifest(ctx context.Context) (Manifest, error) {
	salt, err := readSalt(ctx, s.db)
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{
		Version: 1,
		Salt:    base64.StdEncoding.EncodeToString(salt),
	}, nil
}

func (s *Store) ExportChanges(ctx context.Context) ([]ExportedChange, error) {
	rows, err := s.db.QueryContext(ctx, `
		select change_id, device_id, sequence, task_id, change_type, encrypted_payload, payload_nonce, payload_key_id, created_at
		from task_changes
		order by device_id, sequence`)
	if err != nil {
		return nil, fmt.Errorf("export task changes: %w", err)
	}
	defer rows.Close()
	var changes []ExportedChange
	for rows.Next() {
		var (
			changeID, deviceID, taskID, changeType, payloadKeyID, createdAt string
			sequence                                                        int64
			encryptedPayload, payloadNonce                                  []byte
		)
		if err := rows.Scan(&changeID, &deviceID, &sequence, &taskID, &changeType, &encryptedPayload, &payloadNonce, &payloadKeyID, &createdAt); err != nil {
			return nil, fmt.Errorf("scan exported change: %w", err)
		}
		changes = append(changes, ExportedChange{
			ChangeID:         changeID,
			DeviceID:         deviceID,
			Sequence:         sequence,
			TaskID:           taskID,
			ChangeType:       changeType,
			EncryptedPayload: base64.StdEncoding.EncodeToString(encryptedPayload),
			PayloadNonce:     base64.StdEncoding.EncodeToString(payloadNonce),
			PayloadKeyID:     payloadKeyID,
			CreatedAt:        createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read exported changes: %w", err)
	}
	return changes, nil
}

func (s *Store) MarkChangesSynced(ctx context.Context, changes []ExportedChange) error {
	if len(changes) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mark changes synced: %w", err)
	}
	defer tx.Rollback()
	for _, change := range changes {
		res, err := tx.ExecContext(ctx, `
			update task_changes
			set sync_state = 'synced'
			where change_id = ?`,
			change.ChangeID)
		if err != nil {
			return fmt.Errorf("mark change synced %s: %w", change.ChangeID, err)
		}
		if err := requireAffected(res, change.ChangeID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mark changes synced: %w", err)
	}
	return nil
}

func (s *Store) ImportChanges(ctx context.Context, changes []ExportedChange) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin import changes: %w", err)
	}
	defer tx.Rollback()
	for _, change := range changes {
		conflicted, err := s.detectSequenceConflict(ctx, tx, change)
		if err != nil {
			return err
		}
		if conflicted {
			continue
		}
		encryptedPayload, err := base64.StdEncoding.DecodeString(change.EncryptedPayload)
		if err != nil {
			return fmt.Errorf("decode change payload %s: %w", change.ChangeID, err)
		}
		payloadNonce, err := base64.StdEncoding.DecodeString(change.PayloadNonce)
		if err != nil {
			return fmt.Errorf("decode change nonce %s: %w", change.ChangeID, err)
		}
		_, err = tx.ExecContext(ctx, `
			insert into task_changes(change_id, device_id, sequence, task_id, change_type, encrypted_payload, payload_nonce, payload_key_id, created_at, sync_state)
			values(?, ?, ?, ?, ?, ?, ?, ?, ?, 'synced')
			on conflict(change_id) do nothing`,
			change.ChangeID, change.DeviceID, change.Sequence, change.TaskID, change.ChangeType, encryptedPayload, payloadNonce, change.PayloadKeyID, change.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert imported change %s: %w", change.ChangeID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import changes: %w", err)
	}
	return s.RebuildProjection(ctx)
}

func (s *Store) ListConflicts(ctx context.Context) ([]SyncConflict, error) {
	rows, err := s.db.QueryContext(ctx, `
		select conflict_id, conflict_type, device_id, sequence, local_change_id, remote_change_id, created_at
		from sync_conflicts
		where resolved_at is null
		order by created_at asc`)
	if err != nil {
		return nil, fmt.Errorf("list sync conflicts: %w", err)
	}
	defer rows.Close()
	var conflicts []SyncConflict
	for rows.Next() {
		var conflict SyncConflict
		var createdRaw string
		if err := rows.Scan(
			&conflict.ID,
			&conflict.Type,
			&conflict.DeviceID,
			&conflict.Sequence,
			&conflict.LocalChangeID,
			&conflict.RemoteChangeID,
			&createdRaw,
		); err != nil {
			return nil, fmt.Errorf("scan sync conflict: %w", err)
		}
		createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
		if err != nil {
			return nil, fmt.Errorf("parse sync conflict created_at: %w", err)
		}
		conflict.CreatedAt = createdAt
		conflicts = append(conflicts, conflict)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read sync conflicts: %w", err)
	}
	return conflicts, nil
}

func (s *Store) SealArtifact(name string, plaintext []byte) ([]byte, error) {
	nonce, ciphertext, err := secure.Seal(s.key, plaintext, []byte("artifact:"+name))
	if err != nil {
		return nil, err
	}
	envelope := artifactEnvelope{
		Version:    1,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode artifact envelope: %w", err)
	}
	return data, nil
}

func (s *Store) OpenArtifact(name string, envelopeData []byte) ([]byte, error) {
	var envelope artifactEnvelope
	if err := json.Unmarshal(envelopeData, &envelope); err != nil {
		return nil, fmt.Errorf("decode artifact envelope: %w", err)
	}
	if envelope.Version != 1 {
		return nil, fmt.Errorf("unsupported artifact version: %d", envelope.Version)
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode artifact nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode artifact ciphertext: %w", err)
	}
	return secure.Open(s.key, nonce, ciphertext, []byte("artifact:"+name))
}

func (s *Store) AddTask(ctx context.Context, title, body string) (Task, error) {
	return s.AddTaskWithInput(ctx, TaskInput{Title: title, Body: body})
}

func (s *Store) AddTaskWithInput(ctx context.Context, input TaskInput) (Task, error) {
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		return Task{}, errors.New("title is required")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	task := Task{
		ID:         newID("task"),
		Title:      input.Title,
		Body:       input.Body,
		Tags:       []string{},
		DueAt:      cloneTime(input.DueAt),
		ReminderAt: cloneTime(input.ReminderAt),
		Status:     "open",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	nonce, ciphertext, err := encryptTask(s.key, task.ID, task.toPayload())
	if err != nil {
		return Task{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin add task: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		insert into tasks(task_id, encrypted_payload, payload_nonce, payload_key_id, status_metadata, created_at, updated_at)
		values(?, ?, ?, ?, ?, ?, ?)`,
		task.ID, ciphertext, nonce, payloadKeyID, task.Status, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return Task{}, fmt.Errorf("insert task: %w", err)
	}
	if _, err := s.appendChange(ctx, tx, task.ID, "task.created", changePayload{
		Title:      task.Title,
		Body:       task.Body,
		Tags:       task.Tags,
		DueAt:      task.DueAt,
		ReminderAt: task.ReminderAt,
		Status:     task.Status,
	}, now); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit add task: %w", err)
	}
	return task, nil
}

func (s *Store) EditTask(ctx context.Context, id, title, body string) (Task, error) {
	return s.EditTaskWithInput(ctx, id, TaskInput{Title: title, Body: body})
}

func (s *Store) EditTaskWithInput(ctx context.Context, id string, input TaskInput) (Task, error) {
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		return Task{}, errors.New("title is required")
	}
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	current.Title = input.Title
	current.Body = input.Body
	current.DueAt = cloneTime(input.DueAt)
	current.ReminderAt = cloneTime(input.ReminderAt)
	current.UpdatedAt = now
	nonce, ciphertext, err := encryptTask(s.key, current.ID, current.toPayload())
	if err != nil {
		return Task{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin edit task: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		update tasks
		set encrypted_payload = ?, payload_nonce = ?, payload_key_id = ?, updated_at = ?
		where task_id = ? and deleted_at is null`,
		ciphertext, nonce, payloadKeyID, now.Format(time.RFC3339Nano), id)
	if err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}
	if err := requireAffected(res, id); err != nil {
		return Task{}, err
	}
	if _, err := s.appendChange(ctx, tx, id, "task.updated", changePayload{Title: current.Title, Body: current.Body, Tags: current.Tags, DueAt: current.DueAt, ReminderAt: current.ReminderAt}, now); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit edit task: %w", err)
	}
	return current, nil
}

func (s *Store) AddTag(ctx context.Context, id, tag string) (Task, error) {
	tag = normalizeTag(tag)
	if tag == "" {
		return Task{}, errors.New("tag is required")
	}
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	for _, existing := range current.Tags {
		if existing == tag {
			return current, nil
		}
	}
	current.Tags = append(current.Tags, tag)
	return s.updateTaskTags(ctx, current)
}

func (s *Store) RemoveTag(ctx context.Context, id, tag string) (Task, error) {
	tag = normalizeTag(tag)
	if tag == "" {
		return Task{}, errors.New("tag is required")
	}
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	next := current.Tags[:0]
	for _, existing := range current.Tags {
		if existing != tag {
			next = append(next, existing)
		}
	}
	current.Tags = next
	return s.updateTaskTags(ctx, current)
}

func (s *Store) updateTaskTags(ctx context.Context, task Task) (Task, error) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	task.UpdatedAt = now
	nonce, ciphertext, err := encryptTask(s.key, task.ID, task.toPayload())
	if err != nil {
		return Task{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin tag update: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		update tasks
		set encrypted_payload = ?, payload_nonce = ?, payload_key_id = ?, updated_at = ?
		where task_id = ? and deleted_at is null`,
		ciphertext, nonce, payloadKeyID, now.Format(time.RFC3339Nano), task.ID)
	if err != nil {
		return Task{}, fmt.Errorf("update task tags: %w", err)
	}
	if err := requireAffected(res, task.ID); err != nil {
		return Task{}, err
	}
	if _, err := s.appendChange(ctx, tx, task.ID, "task.tags_changed", changePayload{Tags: task.Tags}, now); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit tag update: %w", err)
	}
	return task, nil
}

func (s *Store) SetTaskStatus(ctx context.Context, id, status string) (Task, error) {
	if status != "open" && status != "done" {
		return Task{}, fmt.Errorf("invalid status: %s", status)
	}
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	current.Status = status
	current.UpdatedAt = now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin status update: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		update tasks
		set status_metadata = ?, updated_at = ?
		where task_id = ? and deleted_at is null`,
		status, now.Format(time.RFC3339Nano), id)
	if err != nil {
		return Task{}, fmt.Errorf("update task status: %w", err)
	}
	if err := requireAffected(res, id); err != nil {
		return Task{}, err
	}
	if _, err := s.appendChange(ctx, tx, id, "task.status_changed", changePayload{Status: status}, now); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit status update: %w", err)
	}
	return current, nil
}

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	if _, err := s.GetTask(ctx, id); err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete task: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		update tasks
		set deleted_at = ?, updated_at = ?
		where task_id = ? and deleted_at is null`,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if err := requireAffected(res, id); err != nil {
		return err
	}
	if _, err := s.appendChange(ctx, tx, id, "task.deleted", changePayload{}, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete task: %w", err)
	}
	return nil
}

func (s *Store) RebuildProjection(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		select change_id, task_id, change_type, encrypted_payload, payload_nonce, created_at
		from task_changes
		order by device_id, sequence`)
	if err != nil {
		return fmt.Errorf("read task changes: %w", err)
	}
	defer rows.Close()

	type replayChange struct {
		id         string
		taskID     string
		changeType string
		payload    changePayload
		createdAt  time.Time
	}
	var changes []replayChange
	for rows.Next() {
		var (
			changeID   string
			taskID     string
			changeType string
			ciphertext []byte
			nonce      []byte
			createdRaw string
		)
		if err := rows.Scan(&changeID, &taskID, &changeType, &ciphertext, &nonce, &createdRaw); err != nil {
			return fmt.Errorf("scan task change: %w", err)
		}
		payload, err := decryptChange(s.key, changeID, nonce, ciphertext)
		if err != nil {
			return err
		}
		createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
		if err != nil {
			return fmt.Errorf("parse change created_at: %w", err)
		}
		changes = append(changes, replayChange{
			id:         changeID,
			taskID:     taskID,
			changeType: changeType,
			payload:    payload,
			createdAt:  createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read task changes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close task changes: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rebuild projection: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `delete from tasks`); err != nil {
		return fmt.Errorf("clear projection: %w", err)
	}
	for _, change := range changes {
		if err := s.replayChange(ctx, tx, change.taskID, change.changeType, change.payload, change.createdAt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rebuild projection: %w", err)
	}
	return nil
}

func (s *Store) ListTasks(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		select task_id, encrypted_payload, payload_nonce, status_metadata, created_at, updated_at
		from tasks
		where deleted_at is null
		order by created_at asc`)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		task, err := s.scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read tasks: %w", err)
	}
	return tasks, nil
}

func (s *Store) GetTask(ctx context.Context, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
		select task_id, encrypted_payload, payload_nonce, status_metadata, created_at, updated_at
		from tasks
		where task_id = ? and deleted_at is null`, id)
	task, err := s.scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, fmt.Errorf("task not found: %s", id)
		}
		return Task{}, err
	}
	return task, nil
}

func (s *Store) SearchTasks(ctx context.Context, query string) ([]Task, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, errors.New("query is required")
	}
	tasks, err := s.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	matches := tasks[:0]
	for _, task := range tasks {
		if strings.Contains(strings.ToLower(task.Title), query) || strings.Contains(strings.ToLower(task.Body), query) || tagsContain(task.Tags, query) {
			matches = append(matches, task)
		}
	}
	return matches, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func (s *Store) scanTask(row scanner) (Task, error) {
	var (
		taskID     string
		ciphertext []byte
		nonce      []byte
		status     string
		createdRaw string
		updatedRaw string
	)
	if err := row.Scan(&taskID, &ciphertext, &nonce, &status, &createdRaw, &updatedRaw); err != nil {
		return Task{}, err
	}
	payload, err := decryptTask(s.key, taskID, nonce, ciphertext)
	if err != nil {
		return Task{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return Task{}, fmt.Errorf("parse created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedRaw)
	if err != nil {
		return Task{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return Task{
		ID:         taskID,
		Title:      payload.Title,
		Body:       payload.Body,
		Tags:       append([]string(nil), payload.Tags...),
		DueAt:      cloneTime(payload.DueAt),
		ReminderAt: cloneTime(payload.ReminderAt),
		Status:     status,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`pragma foreign_keys = on`,
		`create table if not exists app_meta (
			key text primary key,
			value text not null
		)`,
		`create table if not exists tasks (
			task_id text primary key,
			encrypted_payload blob not null,
			payload_nonce blob not null,
			payload_key_id text not null,
			status_metadata text not null,
			created_at text not null,
			updated_at text not null,
			deleted_at text
		)`,
		`create table if not exists task_changes (
			change_id text primary key,
			device_id text not null,
			sequence integer not null,
			task_id text not null,
			change_type text not null,
			encrypted_payload blob not null,
			payload_nonce blob not null,
			payload_key_id text not null,
			created_at text not null,
			sync_state text not null default 'pending',
			unique(device_id, sequence)
		)`,
		`create table if not exists sync_conflicts (
			conflict_id text primary key,
			conflict_type text not null,
			device_id text not null,
			sequence integer not null,
			local_change_id text not null,
			remote_change_id text not null,
			created_at text not null,
			resolved_at text,
			unique(conflict_type, device_id, sequence, local_change_id, remote_change_id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

func (s *Store) detectSequenceConflict(ctx context.Context, tx *sql.Tx, change ExportedChange) (bool, error) {
	var localChangeID string
	err := tx.QueryRowContext(ctx, `
		select change_id from task_changes
		where device_id = ? and sequence = ?`,
		change.DeviceID, change.Sequence).Scan(&localChangeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read sequence conflict candidate: %w", err)
	}
	if localChangeID == change.ChangeID {
		return false, nil
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err = tx.ExecContext(ctx, `
		insert into sync_conflicts(conflict_id, conflict_type, device_id, sequence, local_change_id, remote_change_id, created_at)
		values(?, 'duplicate_device_sequence', ?, ?, ?, ?, ?)
		on conflict(conflict_type, device_id, sequence, local_change_id, remote_change_id) do nothing`,
		newID("conflict"), change.DeviceID, change.Sequence, localChangeID, change.ChangeID, now.Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("record sync conflict: %w", err)
	}
	return true, nil
}

func (s *Store) appendChange(ctx context.Context, tx *sql.Tx, taskID, changeType string, payload changePayload, now time.Time) (Change, error) {
	deviceID, err := s.deviceID(ctx, tx)
	if err != nil {
		return Change{}, err
	}
	sequence, err := s.nextSequence(ctx, tx, deviceID)
	if err != nil {
		return Change{}, err
	}
	change := Change{
		ID:        newID("change"),
		DeviceID:  deviceID,
		Sequence:  sequence,
		TaskID:    taskID,
		Type:      changeType,
		CreatedAt: now,
	}
	nonce, ciphertext, err := encryptChange(s.key, change.ID, payload)
	if err != nil {
		return Change{}, err
	}
	_, err = tx.ExecContext(ctx, `
		insert into task_changes(change_id, device_id, sequence, task_id, change_type, encrypted_payload, payload_nonce, payload_key_id, created_at, sync_state)
		values(?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending')`,
		change.ID, change.DeviceID, change.Sequence, change.TaskID, change.Type, ciphertext, nonce, payloadKeyID, now.Format(time.RFC3339Nano))
	if err != nil {
		return Change{}, fmt.Errorf("insert task change: %w", err)
	}
	return change, nil
}

func (s *Store) deviceID(ctx context.Context, tx *sql.Tx) (string, error) {
	var deviceID string
	if err := tx.QueryRowContext(ctx, `select value from app_meta where key = 'device_id'`).Scan(&deviceID); err != nil {
		return "", fmt.Errorf("read device id: %w", err)
	}
	return deviceID, nil
}

func (s *Store) nextSequence(ctx context.Context, tx *sql.Tx, deviceID string) (int64, error) {
	var sequence sql.NullInt64
	if err := tx.QueryRowContext(ctx, `select max(sequence) from task_changes where device_id = ?`, deviceID).Scan(&sequence); err != nil {
		return 0, fmt.Errorf("read next sequence: %w", err)
	}
	if !sequence.Valid {
		return 1, nil
	}
	return sequence.Int64 + 1, nil
}

func readSalt(ctx context.Context, db *sql.DB) ([]byte, error) {
	var encoded string
	if err := db.QueryRowContext(ctx, `select value from app_meta where key = 'kdf_salt'`).Scan(&encoded); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("database is not initialized")
		}
		return nil, fmt.Errorf("read kdf salt: %w", err)
	}
	salt, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode kdf salt: %w", err)
	}
	return salt, nil
}

func encryptTask(key []byte, taskID string, payload taskPayload) ([]byte, []byte, error) {
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("encode task payload: %w", err)
	}
	return secure.Seal(key, plaintext, []byte("task:"+taskID))
}

func decryptTask(key []byte, taskID string, nonce, ciphertext []byte) (taskPayload, error) {
	plaintext, err := secure.Open(key, nonce, ciphertext, []byte("task:"+taskID))
	if err != nil {
		return taskPayload{}, err
	}
	var payload taskPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return taskPayload{}, fmt.Errorf("decode task payload: %w", err)
	}
	return payload, nil
}

func encryptChange(key []byte, changeID string, payload changePayload) ([]byte, []byte, error) {
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("encode change payload: %w", err)
	}
	return secure.Seal(key, plaintext, []byte("change:"+changeID))
}

func decryptChange(key []byte, changeID string, nonce, ciphertext []byte) (changePayload, error) {
	plaintext, err := secure.Open(key, nonce, ciphertext, []byte("change:"+changeID))
	if err != nil {
		return changePayload{}, err
	}
	var payload changePayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return changePayload{}, fmt.Errorf("decode change payload: %w", err)
	}
	return payload, nil
}

func (s *Store) replayChange(ctx context.Context, tx *sql.Tx, taskID, changeType string, payload changePayload, at time.Time) error {
	switch changeType {
	case "task.created":
		status := payload.Status
		if status == "" {
			status = "open"
		}
		nonce, ciphertext, err := encryptTask(s.key, taskID, taskPayload{Title: payload.Title, Body: payload.Body, Tags: payload.Tags, DueAt: payload.DueAt, ReminderAt: payload.ReminderAt})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			insert into tasks(task_id, encrypted_payload, payload_nonce, payload_key_id, status_metadata, created_at, updated_at)
			values(?, ?, ?, ?, ?, ?, ?)
			on conflict(task_id) do nothing`,
			taskID, ciphertext, nonce, payloadKeyID, status, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("replay create task: %w", err)
		}
		return nil
	case "task.updated":
		current, err := s.taskPayloadInTx(ctx, tx, taskID)
		if err != nil {
			return err
		}
		current.Title = payload.Title
		current.Body = payload.Body
		current.Tags = append([]string(nil), payload.Tags...)
		current.DueAt = cloneTime(payload.DueAt)
		current.ReminderAt = cloneTime(payload.ReminderAt)
		nonce, ciphertext, err := encryptTask(s.key, taskID, current)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			update tasks set encrypted_payload = ?, payload_nonce = ?, payload_key_id = ?, updated_at = ?
			where task_id = ?`,
			ciphertext, nonce, payloadKeyID, at.Format(time.RFC3339Nano), taskID)
		if err != nil {
			return fmt.Errorf("replay update task: %w", err)
		}
		return nil
	case "task.tags_changed":
		current, err := s.taskPayloadInTx(ctx, tx, taskID)
		if err != nil {
			return err
		}
		current.Tags = append([]string(nil), payload.Tags...)
		nonce, ciphertext, err := encryptTask(s.key, taskID, current)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			update tasks set encrypted_payload = ?, payload_nonce = ?, payload_key_id = ?, updated_at = ?
			where task_id = ?`,
			ciphertext, nonce, payloadKeyID, at.Format(time.RFC3339Nano), taskID)
		if err != nil {
			return fmt.Errorf("replay tag change: %w", err)
		}
		return nil
	case "task.status_changed":
		_, err := tx.ExecContext(ctx, `
			update tasks set status_metadata = ?, updated_at = ?
			where task_id = ?`,
			payload.Status, at.Format(time.RFC3339Nano), taskID)
		if err != nil {
			return fmt.Errorf("replay status change: %w", err)
		}
		return nil
	case "task.deleted":
		_, err := tx.ExecContext(ctx, `
			update tasks set deleted_at = ?, updated_at = ?
			where task_id = ?`,
			at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano), taskID)
		if err != nil {
			return fmt.Errorf("replay delete task: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown change type: %s", changeType)
	}
}

func (s *Store) taskPayloadInTx(ctx context.Context, tx *sql.Tx, taskID string) (taskPayload, error) {
	var ciphertext, nonce []byte
	if err := tx.QueryRowContext(ctx, `
		select encrypted_payload, payload_nonce from tasks where task_id = ?`, taskID).Scan(&ciphertext, &nonce); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return taskPayload{}, fmt.Errorf("task not found during replay: %s", taskID)
		}
		return taskPayload{}, fmt.Errorf("read task during replay: %w", err)
	}
	return decryptTask(s.key, taskID, nonce, ciphertext)
}

func requireAffected(res sql.Result, id string) error {
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("task not found: %s", id)
	}
	return nil
}

func normalizeTag(tag string) string {
	tag = strings.TrimSpace(strings.ToLower(tag))
	tag = strings.TrimPrefix(tag, "#")
	return tag
}

func tagsContain(tags []string, query string) bool {
	for _, tag := range tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}

func (t Task) toPayload() taskPayload {
	return taskPayload{
		Title:      t.Title,
		Body:       t.Body,
		Tags:       append([]string(nil), t.Tags...),
		DueAt:      cloneTime(t.DueAt),
		ReminderAt: cloneTime(t.ReminderAt),
	}
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC().Truncate(time.Second)
	return &cloned
}

func newID(prefix string) string {
	random, err := secure.RandomBytes(16)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s_%x", prefix, random)
}
