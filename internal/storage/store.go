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
	ID        string
	Title     string
	Body      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type taskPayload struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

func Init(ctx context.Context, path string, recoverySecret string) error {
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
	salt, err := secure.RandomBytes(secure.SaltSize)
	if err != nil {
		return err
	}
	if _, err := secure.DeriveKey(recoverySecret, salt, secure.DefaultKDFParams()); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `insert into app_meta(key, value) values('kdf_salt', ?)`, base64.StdEncoding.EncodeToString(salt))
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

func (s *Store) AddTask(ctx context.Context, title, body string) (Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, errors.New("title is required")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	task := Task{
		ID:        newID("task"),
		Title:     title,
		Body:      body,
		Status:    "open",
		CreatedAt: now,
		UpdatedAt: now,
	}
	nonce, ciphertext, err := encryptTask(s.key, task.ID, taskPayload{Title: title, Body: body})
	if err != nil {
		return Task{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into tasks(task_id, encrypted_payload, payload_nonce, payload_key_id, status_metadata, created_at, updated_at)
		values(?, ?, ?, ?, ?, ?, ?)`,
		task.ID, ciphertext, nonce, payloadKeyID, task.Status, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return Task{}, fmt.Errorf("insert task: %w", err)
	}
	return task, nil
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
		if strings.Contains(strings.ToLower(task.Title), query) || strings.Contains(strings.ToLower(task.Body), query) {
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
		ID:        taskID,
		Title:     payload.Title,
		Body:      payload.Body,
		Status:    status,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
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
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
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

func newID(prefix string) string {
	random, err := secure.RandomBytes(16)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s_%x", prefix, random)
}
