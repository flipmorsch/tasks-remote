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
	"sort"
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

type PlaintextExport struct {
	Version     int       `json:"version"`
	ExportedAt  time.Time `json:"exported_at"`
	Warning     string    `json:"warning"`
	ActiveTasks []Task    `json:"active_tasks"`
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
	TaskID         string
	DeviceID       string
	Sequence       int64
	LocalChangeID  string
	RemoteChangeID string
	CreatedAt      time.Time
}

// ConflictSide is one of the two divergent changes in a Sync Conflict, decoded
// for display so the user can tell the versions apart before choosing.
type ConflictSide struct {
	Label      string // "local" or "remote": the keyword passed to resolve
	ChangeID   string
	DeviceID   string
	ChangeType string
	Present    bool // false when the change was not stored (e.g. a rejected duplicate)
	Deleted    bool
	Title      string
	Body       string
	Tags       []string
}

// ConflictDetail is a Sync Conflict with both sides decoded for the user.
type ConflictDetail struct {
	SyncConflict
	Local  ConflictSide
	Remote ConflictSide
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
	ParentChangeID   string `json:"parent_change_id,omitempty"`
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
	// Deleted marks a task.resolved change that resolves a conflict by deleting
	// the task rather than keeping a content version.
	Deleted bool `json:"deleted,omitempty"`
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

// LocalDeviceID returns this device's stable identifier, assigned at init and
// used to namespace its append-safe change artifact during sync.
func (s *Store) LocalDeviceID(ctx context.Context) (string, error) {
	var deviceID string
	if err := s.db.QueryRowContext(ctx, `select value from app_meta where key = 'device_id'`).Scan(&deviceID); err != nil {
		return "", fmt.Errorf("read device id: %w", err)
	}
	return deviceID, nil
}

func (s *Store) ExportChanges(ctx context.Context) ([]ExportedChange, error) {
	rows, err := s.db.QueryContext(ctx, `
		select change_id, device_id, sequence, task_id, change_type, parent_change_id, encrypted_payload, payload_nonce, payload_key_id, created_at
		from task_changes
		order by device_id, sequence`)
	if err != nil {
		return nil, fmt.Errorf("export task changes: %w", err)
	}
	defer rows.Close()
	return scanExportedChanges(rows)
}

// ExportDeviceChanges returns only the changes that originated on the given
// device. Each device pushes its own changes to its own cloud artifact, so a
// push never has to rewrite another device's history.
func (s *Store) ExportDeviceChanges(ctx context.Context, deviceID string) ([]ExportedChange, error) {
	rows, err := s.db.QueryContext(ctx, `
		select change_id, device_id, sequence, task_id, change_type, parent_change_id, encrypted_payload, payload_nonce, payload_key_id, created_at
		from task_changes
		where device_id = ?
		order by sequence`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("export device task changes: %w", err)
	}
	defer rows.Close()
	return scanExportedChanges(rows)
}

func scanExportedChanges(rows *sql.Rows) ([]ExportedChange, error) {
	var changes []ExportedChange
	for rows.Next() {
		var (
			changeID, deviceID, taskID, changeType, parentChangeID, payloadKeyID, createdAt string
			sequence                                                                        int64
			encryptedPayload, payloadNonce                                                  []byte
		)
		if err := rows.Scan(&changeID, &deviceID, &sequence, &taskID, &changeType, &parentChangeID, &encryptedPayload, &payloadNonce, &payloadKeyID, &createdAt); err != nil {
			return nil, fmt.Errorf("scan exported change: %w", err)
		}
		changes = append(changes, ExportedChange{
			ChangeID:         changeID,
			DeviceID:         deviceID,
			Sequence:         sequence,
			TaskID:           taskID,
			ChangeType:       changeType,
			ParentChangeID:   parentChangeID,
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
			insert into task_changes(change_id, device_id, sequence, task_id, change_type, parent_change_id, encrypted_payload, payload_nonce, payload_key_id, created_at, sync_state)
			values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'synced')
			on conflict(change_id) do nothing`,
			change.ChangeID, change.DeviceID, change.Sequence, change.TaskID, change.ChangeType, change.ParentChangeID, encryptedPayload, payloadNonce, change.PayloadKeyID, change.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert imported change %s: %w", change.ChangeID, err)
		}
	}
	if err := s.reconcileForkConflicts(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import changes: %w", err)
	}
	return s.RebuildProjection(ctx)
}

func (s *Store) ListConflicts(ctx context.Context) ([]SyncConflict, error) {
	rows, err := s.db.QueryContext(ctx, `
		select conflict_id, conflict_type, task_id, device_id, sequence, local_change_id, remote_change_id, created_at
		from sync_conflicts
		where resolved_at is null
		order by created_at asc`)
	if err != nil {
		return nil, fmt.Errorf("list sync conflicts: %w", err)
	}
	defer rows.Close()
	var conflicts []SyncConflict
	for rows.Next() {
		conflict, err := scanConflict(rows)
		if err != nil {
			return nil, err
		}
		conflicts = append(conflicts, conflict)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read sync conflicts: %w", err)
	}
	return conflicts, nil
}

func scanConflict(row scanner) (SyncConflict, error) {
	var conflict SyncConflict
	var createdRaw string
	if err := row.Scan(
		&conflict.ID,
		&conflict.Type,
		&conflict.TaskID,
		&conflict.DeviceID,
		&conflict.Sequence,
		&conflict.LocalChangeID,
		&conflict.RemoteChangeID,
		&createdRaw,
	); err != nil {
		return SyncConflict{}, fmt.Errorf("scan sync conflict: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return SyncConflict{}, fmt.Errorf("parse sync conflict created_at: %w", err)
	}
	conflict.CreatedAt = createdAt
	return conflict, nil
}

// ListConflictDetails returns each open conflict with both sides decrypted so
// the caller can present the divergent versions to the user.
func (s *Store) ListConflictDetails(ctx context.Context) ([]ConflictDetail, error) {
	conflicts, err := s.ListConflicts(ctx)
	if err != nil {
		return nil, err
	}
	details := make([]ConflictDetail, 0, len(conflicts))
	for _, conflict := range conflicts {
		detail, err := s.conflictDetail(ctx, conflict)
		if err != nil {
			return nil, err
		}
		details = append(details, detail)
	}
	return details, nil
}

func (s *Store) conflictDetail(ctx context.Context, conflict SyncConflict) (ConflictDetail, error) {
	local, err := s.conflictSide(ctx, "local", conflict.LocalChangeID)
	if err != nil {
		return ConflictDetail{}, err
	}
	remote, err := s.conflictSide(ctx, "remote", conflict.RemoteChangeID)
	if err != nil {
		return ConflictDetail{}, err
	}
	return ConflictDetail{SyncConflict: conflict, Local: local, Remote: remote}, nil
}

func (s *Store) conflictSide(ctx context.Context, label, changeID string) (ConflictSide, error) {
	side := ConflictSide{Label: label, ChangeID: changeID}
	var (
		deviceID, changeType string
		ciphertext, nonce    []byte
	)
	err := s.db.QueryRowContext(ctx, `
		select device_id, change_type, encrypted_payload, payload_nonce
		from task_changes where change_id = ?`, changeID).Scan(&deviceID, &changeType, &ciphertext, &nonce)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// A rejected duplicate is recorded as a conflict but never stored.
			return side, nil
		}
		return ConflictSide{}, fmt.Errorf("read conflict side %s: %w", changeID, err)
	}
	side.Present = true
	side.DeviceID = deviceID
	side.ChangeType = changeType
	side.Deleted = changeType == "task.deleted"
	if changeType != "task.deleted" {
		payload, err := decryptChange(s.key, changeID, nonce, ciphertext)
		if err != nil {
			return ConflictSide{}, err
		}
		side.Title = payload.Title
		side.Body = payload.Body
		side.Tags = append([]string(nil), payload.Tags...)
	}
	return side, nil
}

// ResolveConflict records the user's decision for a conflict. For content and
// delete/edit conflicts it appends a task.resolved change carrying the chosen
// side, which becomes the newest change and so wins replay everywhere once it
// syncs. The conflict is marked resolved locally immediately. `use` is "local"
// or "remote"; for a duplicate-sequence conflict it is ignored and the conflict
// is simply dismissed (the duplicate was never applied).
func (s *Store) ResolveConflict(ctx context.Context, conflictID, use string) error {
	row := s.db.QueryRowContext(ctx, `
		select conflict_id, conflict_type, task_id, device_id, sequence, local_change_id, remote_change_id, created_at
		from sync_conflicts
		where conflict_id = ? and resolved_at is null`, conflictID)
	conflict, err := scanConflict(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("open conflict not found: %s", conflictID)
		}
		return err
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin resolve conflict: %w", err)
	}
	defer tx.Rollback()

	var resolvedChangeID sql.NullString
	if conflict.Type != "duplicate_device_sequence" {
		chosenID, err := chosenSide(conflict, use)
		if err != nil {
			return err
		}
		resolution, err := s.resolutionPayload(ctx, tx, chosenID)
		if err != nil {
			return err
		}
		change, err := s.appendChangeWithParent(ctx, tx, conflict.TaskID, "task.resolved", resolution, chosenID, now)
		if err != nil {
			return err
		}
		resolvedChangeID = sql.NullString{String: change.ID, Valid: true}
	}

	if _, err := tx.ExecContext(ctx, `
		update sync_conflicts
		set resolved_at = ?, resolved_change_id = ?
		where conflict_id = ?`,
		now.Format(time.RFC3339Nano), resolvedChangeID, conflictID); err != nil {
		return fmt.Errorf("mark conflict resolved: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit resolve conflict: %w", err)
	}
	return s.RebuildProjection(ctx)
}

func chosenSide(conflict SyncConflict, use string) (string, error) {
	switch use {
	case "local":
		return conflict.LocalChangeID, nil
	case "remote":
		return conflict.RemoteChangeID, nil
	case conflict.LocalChangeID, conflict.RemoteChangeID:
		return use, nil
	default:
		return "", fmt.Errorf("choose a side with local or remote")
	}
}

// resolutionPayload reads the chosen change and turns it into the payload for a
// task.resolved change: the chosen content, or a deletion marker.
func (s *Store) resolutionPayload(ctx context.Context, tx *sql.Tx, changeID string) (changePayload, error) {
	var changeType string
	var ciphertext, nonce []byte
	err := tx.QueryRowContext(ctx, `
		select change_type, encrypted_payload, payload_nonce
		from task_changes where change_id = ?`, changeID).Scan(&changeType, &ciphertext, &nonce)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return changePayload{}, fmt.Errorf("chosen change not found: %s", changeID)
		}
		return changePayload{}, fmt.Errorf("read chosen change: %w", err)
	}
	if changeType == "task.deleted" {
		return changePayload{Deleted: true}, nil
	}
	payload, err := decryptChange(s.key, changeID, nonce, ciphertext)
	if err != nil {
		return changePayload{}, err
	}
	return changePayload{
		Title:      payload.Title,
		Body:       payload.Body,
		Tags:       payload.Tags,
		DueAt:      payload.DueAt,
		ReminderAt: payload.ReminderAt,
	}, nil
}

// DueReminder is an active task with a reminder that is due or upcoming.
type DueReminder struct {
	Task Task
	// Due is true when the reminder time has already passed, false when it
	// falls inside the upcoming window.
	Due bool
}

// Reminders returns active (open, not deleted) tasks whose reminder time has
// passed as of asOf, or falls within the upcoming window, sorted soonest
// first. It is the read side of reminder delivery; surfacing or notifying is
// left to the caller.
func (s *Store) Reminders(ctx context.Context, asOf time.Time, within time.Duration) ([]DueReminder, error) {
	tasks, err := s.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	cutoff := asOf.Add(within)
	var reminders []DueReminder
	for _, task := range tasks {
		if task.Status == "done" || task.ReminderAt == nil {
			continue
		}
		remindAt := *task.ReminderAt
		switch {
		case !remindAt.After(asOf):
			reminders = append(reminders, DueReminder{Task: task, Due: true})
		case !remindAt.After(cutoff):
			reminders = append(reminders, DueReminder{Task: task, Due: false})
		}
	}
	sort.Slice(reminders, func(i, j int) bool {
		return reminders[i].Task.ReminderAt.Before(*reminders[j].Task.ReminderAt)
	})
	return reminders, nil
}

func (s *Store) ExportPlaintext(ctx context.Context) (PlaintextExport, error) {
	tasks, err := s.ListTasks(ctx)
	if err != nil {
		return PlaintextExport{}, err
	}
	return PlaintextExport{
		Version:     1,
		ExportedAt:  time.Now().UTC().Truncate(time.Second),
		Warning:     "This file contains plaintext Sensitive Task Data.",
		ActiveTasks: tasks,
	}, nil
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
	// Replay in temporal order so the most recent edit by wall-clock wins.
	// device_id and sequence only break ties deterministically. This makes a
	// conflict resolution (a newer task.resolved change) supersede the forked
	// edits it resolves, and keeps multi-device merges intuitive.
	rows, err := s.db.QueryContext(ctx, `
		select change_id, task_id, change_type, encrypted_payload, payload_nonce, created_at
		from task_changes
		order by created_at, device_id, sequence`)
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
			parent_change_id text not null default '',
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
			task_id text not null default '',
			device_id text not null,
			sequence integer not null,
			local_change_id text not null,
			remote_change_id text not null,
			resolved_change_id text,
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
	// Additive column upgrades for databases created before these columns
	// existed. SQLite cannot add them through `create table if not exists`.
	columns := []struct{ table, column, ddl string }{
		{"task_changes", "parent_change_id", "parent_change_id text not null default ''"},
		{"sync_conflicts", "task_id", "task_id text not null default ''"},
		{"sync_conflicts", "resolved_change_id", "resolved_change_id text"},
	}
	for _, c := range columns {
		if err := ensureColumn(ctx, db, c.table, c.column, c.ddl); err != nil {
			return err
		}
	}
	return nil
}

func ensureColumn(ctx context.Context, db *sql.DB, table, column, ddl string) error {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("pragma table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan %s column info: %w", table, err)
		}
		if name == column {
			return rows.Close()
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read %s column info: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close %s column info: %w", table, err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("alter table %s add column %s", table, ddl)); err != nil {
		return fmt.Errorf("add %s.%s column: %w", table, column, err)
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
		insert into sync_conflicts(conflict_id, conflict_type, task_id, device_id, sequence, local_change_id, remote_change_id, created_at)
		values(?, 'duplicate_device_sequence', ?, ?, ?, ?, ?, ?)
		on conflict(conflict_type, device_id, sequence, local_change_id, remote_change_id) do nothing`,
		newID("conflict"), change.TaskID, change.DeviceID, change.Sequence, localChangeID, change.ChangeID, now.Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("record sync conflict: %w", err)
	}
	return true, nil
}

// reconcileForkConflicts records and resolves divergence in each task's change
// chain. A fork is two or more changes that share a parent — i.e. two devices
// edited the same version of a task offline. Concurrent content edits and
// delete/edit collisions become user-resolvable Sync Conflicts; lower-stakes
// forks (status, tags) fall through to the temporal last-writer-wins replay.
//
// An open conflict is auto-resolved once a `task.resolved` change descends from
// either side, which is how a resolution made on one device converges to the
// others when its resolution change syncs in.
func (s *Store) reconcileForkConflicts(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
		select task_id, parent_change_id
		from task_changes
		where parent_change_id != ''
		group by task_id, parent_change_id
		having count(*) > 1`)
	if err != nil {
		return fmt.Errorf("scan task change forks: %w", err)
	}
	type fork struct{ taskID, parent string }
	var forks []fork
	for rows.Next() {
		var f fork
		if err := rows.Scan(&f.taskID, &f.parent); err != nil {
			rows.Close()
			return fmt.Errorf("scan fork: %w", err)
		}
		forks = append(forks, f)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read forks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close forks: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, f := range forks {
		children, err := s.forkChildren(ctx, tx, f.taskID, f.parent)
		if err != nil {
			return err
		}
		if len(children) < 2 {
			continue
		}
		anchor := children[0]
		for _, other := range children[1:] {
			conflictType := classifyFork(anchor.changeType, other.changeType)
			if conflictType == "" {
				continue
			}
			_, err := tx.ExecContext(ctx, `
				insert into sync_conflicts(conflict_id, conflict_type, task_id, device_id, sequence, local_change_id, remote_change_id, created_at)
				values(?, ?, ?, ?, ?, ?, ?, ?)
				on conflict(conflict_type, device_id, sequence, local_change_id, remote_change_id) do nothing`,
				newID("conflict"), conflictType, f.taskID, other.deviceID, other.sequence, anchor.changeID, other.changeID, now.Format(time.RFC3339Nano))
			if err != nil {
				return fmt.Errorf("record fork conflict: %w", err)
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `
		update sync_conflicts
		set resolved_at = ?
		where resolved_at is null
		  and conflict_type in ('concurrent_edit', 'delete_edit')
		  and exists (
			select 1 from task_changes tc
			where tc.change_type = 'task.resolved'
			  and (tc.parent_change_id = sync_conflicts.local_change_id
				or tc.parent_change_id = sync_conflicts.remote_change_id))`,
		now.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("auto-resolve fork conflicts: %w", err)
	}
	return nil
}

type forkChild struct {
	changeID   string
	deviceID   string
	sequence   int64
	changeType string
}

func (s *Store) forkChildren(ctx context.Context, tx *sql.Tx, taskID, parent string) ([]forkChild, error) {
	rows, err := tx.QueryContext(ctx, `
		select change_id, device_id, sequence, change_type
		from task_changes
		where task_id = ? and parent_change_id = ?
		order by change_id`, taskID, parent)
	if err != nil {
		return nil, fmt.Errorf("read fork children: %w", err)
	}
	defer rows.Close()
	var children []forkChild
	for rows.Next() {
		var c forkChild
		if err := rows.Scan(&c.changeID, &c.deviceID, &c.sequence, &c.changeType); err != nil {
			return nil, fmt.Errorf("scan fork child: %w", err)
		}
		children = append(children, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read fork children: %w", err)
	}
	return children, nil
}

// classifyFork names the Sync Conflict for two changes that share a parent, or
// returns "" when the divergence is safe to auto-merge.
func classifyFork(a, b string) string {
	if a == "task.deleted" || b == "task.deleted" {
		return "delete_edit"
	}
	if a == "task.updated" && b == "task.updated" {
		return "concurrent_edit"
	}
	return ""
}

func (s *Store) appendChange(ctx context.Context, tx *sql.Tx, taskID, changeType string, payload changePayload, now time.Time) (Change, error) {
	parent, err := s.taskHead(ctx, tx, taskID)
	if err != nil {
		return Change{}, err
	}
	return s.appendChangeWithParent(ctx, tx, taskID, changeType, payload, parent, now)
}

// appendChangeWithParent records a change whose parent is set explicitly rather
// than inferred from the current head. Conflict resolution uses this to descend
// from the specific side it resolves.
func (s *Store) appendChangeWithParent(ctx context.Context, tx *sql.Tx, taskID, changeType string, payload changePayload, parent string, now time.Time) (Change, error) {
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
		insert into task_changes(change_id, device_id, sequence, task_id, change_type, parent_change_id, encrypted_payload, payload_nonce, payload_key_id, created_at, sync_state)
		values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending')`,
		change.ID, change.DeviceID, change.Sequence, change.TaskID, change.Type, parent, ciphertext, nonce, payloadKeyID, now.Format(time.RFC3339Nano))
	if err != nil {
		return Change{}, fmt.Errorf("insert task change: %w", err)
	}
	return change, nil
}

// taskHead returns the change_id of the most recent change this device has
// applied to a task, in the same temporal order replay uses. A new edit
// records that change as its parent, so an edit made against an already-merged
// state stays linear, while two edits made from the same base fork apart and
// surface as a Sync Conflict.
func (s *Store) taskHead(ctx context.Context, tx *sql.Tx, taskID string) (string, error) {
	var head string
	err := tx.QueryRowContext(ctx, `
		select change_id from task_changes
		where task_id = ?
		order by created_at desc, device_id desc, sequence desc
		limit 1`, taskID).Scan(&head)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("read task head: %w", err)
	}
	return head, nil
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
	case "task.resolved":
		if payload.Deleted {
			_, err := tx.ExecContext(ctx, `
				update tasks set deleted_at = ?, updated_at = ?
				where task_id = ?`,
				at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano), taskID)
			if err != nil {
				return fmt.Errorf("replay resolve delete: %w", err)
			}
			return nil
		}
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
		// A content resolution also revives a task that the losing side had
		// deleted, so choosing the edit over a delete restores the task.
		_, err = tx.ExecContext(ctx, `
			update tasks set encrypted_payload = ?, payload_nonce = ?, payload_key_id = ?, deleted_at = null, updated_at = ?
			where task_id = ?`,
			ciphertext, nonce, payloadKeyID, at.Format(time.RFC3339Nano), taskID)
		if err != nil {
			return fmt.Errorf("replay resolve task: %w", err)
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
