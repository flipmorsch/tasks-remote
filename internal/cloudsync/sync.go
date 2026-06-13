package cloudsync

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tasks-remote/internal/storage"
)

const (
	ManifestName = "manifest.json"
	// DevicePrefix is the cloud namespace under which each device writes its
	// own append-safe change artifact. A device only ever writes to its own
	// path, so concurrent pushes from different devices cannot overwrite each
	// other. See ADR 0002 and SPEC.md "Sync Format".
	DevicePrefix = "devices/"
)

// DeviceChangesName is the artifact path a device writes its own changes to.
// The path encodes the device id only for routing; security still comes from
// the authenticated payload, never from the filename (SPEC.md).
func DeviceChangesName(deviceID string) string {
	return DevicePrefix + deviceID + "/changes-v1.json.enc"
}

type Client interface {
	Put(ctx context.Context, name string, data []byte) error
	Get(ctx context.Context, name string) ([]byte, error)
	// List returns the names of all artifacts whose name begins with prefix,
	// sorted lexicographically. A missing namespace returns an empty list,
	// not an error.
	List(ctx context.Context, prefix string) ([]string, error)
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
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create sync directory: %w", err)
	}
	// Write to a temp file and rename into place so an interrupted or crashed
	// push can never leave a half-written artifact that would later fail to
	// authenticate and block pulls. The rename is atomic within the directory.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp sync artifact %s: %w", name, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temp sync artifact %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write sync artifact %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("flush sync artifact %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close sync artifact %s: %w", name, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("commit sync artifact %s: %w", name, err)
	}
	cleanup = false
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

func (c LocalDirClient) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.Dir == "" {
		return nil, fmt.Errorf("sync directory is required")
	}
	if _, err := os.Stat(c.Dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat sync directory: %w", err)
	}
	var names []string
	walkErr := filepath.WalkDir(c.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Skip in-progress temp artifacts (and any dotfile) so an interrupted
		// write is never mistaken for a real artifact during a pull.
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, err := filepath.Rel(c.Dir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("list sync artifacts: %w", walkErr)
	}
	sort.Strings(names)
	return names, nil
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
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || filepath.IsAbs(clean) || clean != name || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("invalid sync artifact name: %s", name)
	}
	return nil
}

// Push uploads the manifest and this device's own change artifact. It only
// writes the calling device's namespace, so two devices syncing at the same
// time never clobber each other's uploaded changes.
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
	deviceID, err := store.LocalDeviceID(ctx)
	if err != nil {
		return err
	}
	changes, err := store.ExportDeviceChanges(ctx, deviceID)
	if err != nil {
		return err
	}
	name := DeviceChangesName(deviceID)
	changesData, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sync changes: %w", err)
	}
	artifact, err := store.SealArtifact(name, changesData)
	if err != nil {
		return err
	}
	if err := client.Put(ctx, name, artifact); err != nil {
		return err
	}
	return store.MarkChangesSynced(ctx, changes)
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

// Pull downloads every device's change artifact, decrypts and authenticates
// each one, and imports the merged set. Each artifact is bound to its own path
// through authenticated associated data, so a swapped or tampered file fails
// to open rather than silently corrupting the merge.
func Pull(ctx context.Context, store *storage.Store, client Client) error {
	names, err := client.List(ctx, DevicePrefix)
	if err != nil {
		return err
	}
	sort.Strings(names)
	var merged []storage.ExportedChange
	for _, name := range names {
		artifact, err := client.Get(ctx, name)
		if err != nil {
			return err
		}
		changesData, err := store.OpenArtifact(name, artifact)
		if err != nil {
			return fmt.Errorf("open device artifact %s: %w", name, err)
		}
		var changes []storage.ExportedChange
		if err := json.Unmarshal(changesData, &changes); err != nil {
			return fmt.Errorf("decode device artifact %s: %w", name, err)
		}
		merged = append(merged, changes...)
	}
	return store.ImportChanges(ctx, merged)
}
