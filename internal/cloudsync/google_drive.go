package cloudsync

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

type GoogleDriveClient struct {
	Service *drive.Service
}

func (c GoogleDriveClient) Put(ctx context.Context, name string, data []byte) error {
	if c.Service == nil {
		return fmt.Errorf("google drive service is required")
	}
	if err := validateArtifactName(name); err != nil {
		return err
	}
	fileID, err := c.find(ctx, name)
	if err != nil {
		return err
	}
	media := bytes.NewReader(data)
	if fileID == "" {
		_, err = c.Service.Files.Create(&drive.File{
			Name:    name,
			Parents: []string{"appDataFolder"},
		}).Media(media, googleapi.ContentType("application/octet-stream")).Fields("id").Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("create drive artifact %s: %w", name, err)
		}
		return nil
	}
	_, err = c.Service.Files.Update(fileID, &drive.File{}).
		Media(media, googleapi.ContentType("application/octet-stream")).
		Fields("id").
		Context(ctx).
		Do()
	if err != nil {
		return fmt.Errorf("update drive artifact %s: %w", name, err)
	}
	return nil
}

func (c GoogleDriveClient) Get(ctx context.Context, name string) ([]byte, error) {
	if c.Service == nil {
		return nil, fmt.Errorf("google drive service is required")
	}
	if err := validateArtifactName(name); err != nil {
		return nil, err
	}
	fileID, err := c.find(ctx, name)
	if err != nil {
		return nil, err
	}
	if fileID == "" {
		return nil, fmt.Errorf("drive artifact not found: %s", name)
	}
	resp, err := c.Service.Files.Get(fileID).Download()
	if err != nil {
		return nil, fmt.Errorf("download drive artifact %s: %w", name, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read drive artifact %s: %w", name, err)
	}
	return data, nil
}

func (c GoogleDriveClient) find(ctx context.Context, name string) (string, error) {
	escaped := strings.ReplaceAll(name, "'", "\\'")
	res, err := c.Service.Files.List().
		Spaces("appDataFolder").
		Q(fmt.Sprintf("name = '%s' and trashed = false", escaped)).
		Fields("files(id, name)").
		PageSize(1).
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("find drive artifact %s: %w", name, err)
	}
	if len(res.Files) == 0 {
		return "", nil
	}
	return res.Files[0].Id, nil
}
