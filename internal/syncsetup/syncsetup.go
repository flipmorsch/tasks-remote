package syncsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Kind string

const (
	None   Kind = ""
	Google Kind = "google"
	Dir    Kind = "dir"
)

type Config struct {
	Kind            Kind   `json:"kind"`
	CredentialsPath string `json:"credentials_path,omitempty"`
	Dir             string `json:"dir,omitempty"`
}

type fileData struct {
	ByDatabase map[string]Config `json:"by_database"`
}

func Load(dbPath string) (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var file fileData
	if err := json.Unmarshal(data, &file); err != nil {
		return Config{}, fmt.Errorf("decode Local Sync Setup: %w", err)
	}
	return file.ByDatabase[dbPath], nil
}

func Save(dbPath string, cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	file := fileData{ByDatabase: map[string]Config{}}
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &file)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if file.ByDatabase == nil {
		file.ByDatabase = map[string]Config{}
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

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tasks-remote", "sync.json"), nil
}
