package cloudsync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"tasks-remote/internal/storage"
)

const (
	ManifestName = "manifest.json"
	ChangesName  = "changes-v1.json.enc"
)

type Client interface {
	Put(ctx context.Context, name string, data []byte) error
	Get(ctx context.Context, name string) ([]byte, error)
}

type LocalDirClient struct {
	Dir string
}

func (c LocalDirClient) Put(ctx context.Context, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := c.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create sync directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write sync artifact %s: %w", name, err)
	}
	return nil
}

func (c LocalDirClient) Get(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := c.path(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sync artifact %s: %w", name, err)
	}
	return data, nil
}

func (c LocalDirClient) path(name string) (string, error) {
	if c.Dir == "" {
		return "", fmt.Errorf("sync directory is required")
	}
	if err := validateArtifactName(name); err != nil {
		return "", err
	}
	clean := filepath.Clean(name)
	return filepath.Join(c.Dir, clean), nil
}

func validateArtifactName(name string) error {
	clean := filepath.Clean(name)
	if clean == "." || filepath.IsAbs(clean) || clean != name {
		return fmt.Errorf("invalid sync artifact name: %s", name)
	}
	return nil
}

func Push(ctx context.Context, store *storage.Store, client Client) error {
	manifest, err := store.Manifest(ctx)
	if err != nil {
		return err
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sync manifest: %w", err)
	}
	if err := client.Put(ctx, ManifestName, manifestData); err != nil {
		return err
	}
	changes, err := store.ExportChanges(ctx)
	if err != nil {
		return err
	}
	changesData, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sync changes: %w", err)
	}
	artifact, err := store.SealArtifact(ChangesName, changesData)
	if err != nil {
		return err
	}
	return client.Put(ctx, ChangesName, artifact)
}

func ReadManifest(ctx context.Context, client Client) (storage.Manifest, error) {
	data, err := client.Get(ctx, ManifestName)
	if err != nil {
		return storage.Manifest{}, err
	}
	var manifest storage.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return storage.Manifest{}, fmt.Errorf("decode sync manifest: %w", err)
	}
	if manifest.Version != 1 {
		return storage.Manifest{}, fmt.Errorf("unsupported manifest version: %d", manifest.Version)
	}
	if manifest.Salt == "" {
		return storage.Manifest{}, fmt.Errorf("manifest salt is required")
	}
	return manifest, nil
}

func Pull(ctx context.Context, store *storage.Store, client Client) error {
	artifact, err := client.Get(ctx, ChangesName)
	if err != nil {
		return err
	}
	changesData, err := store.OpenArtifact(ChangesName, artifact)
	if err != nil {
		return err
	}
	var changes []storage.ExportedChange
	if err := json.Unmarshal(changesData, &changes); err != nil {
		return fmt.Errorf("decode sync changes: %w", err)
	}
	return store.ImportChanges(ctx, changes)
}
