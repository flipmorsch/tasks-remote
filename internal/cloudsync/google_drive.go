package cloudsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/oauth2"
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
			return classifyDriveError("upload "+name, err)
		}
		return nil
	}
	_, err = c.Service.Files.Update(fileID, &drive.File{}).
		Media(media, googleapi.ContentType("application/octet-stream")).
		Fields("id").
		Context(ctx).
		Do()
	if err != nil {
		return classifyDriveError("upload "+name, err)
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
		return nil, classifyDriveError("download "+name, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read drive artifact %s: %w", name, err)
	}
	return data, nil
}

func (c GoogleDriveClient) List(ctx context.Context, prefix string) ([]string, error) {
	if c.Service == nil {
		return nil, fmt.Errorf("google drive service is required")
	}
	var names []string
	pageToken := ""
	for {
		call := c.Service.Files.List().
			Spaces("appDataFolder").
			Q("trashed = false").
			Fields("nextPageToken, files(name)").
			PageSize(1000).
			Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		res, err := call.Do()
		if err != nil {
			return nil, classifyDriveError("list drive artifacts", err)
		}
		for _, file := range res.Files {
			if strings.HasPrefix(file.Name, prefix) {
				names = append(names, file.Name)
			}
		}
		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}
	sort.Strings(names)
	return names, nil
}

// classifyDriveError turns a raw Drive/OAuth error into an actionable, secret-
// free message. It addresses the SPEC threat of visible-but-degraded sync:
// auth loss, quota/rate limits, and connectivity all produce a clear next step
// rather than a raw API error. The underlying error is still wrapped for logs.
func classifyDriveError(op string, err error) error {
	if err == nil {
		return nil
	}

	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) {
		return fmt.Errorf("%s: Google authorization is no longer valid; run `tasks-remote login google` to reauthorize: %w", op, err)
	}

	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == 401:
			return fmt.Errorf("%s: Google authorization expired or was revoked; run `tasks-remote login google` to reauthorize: %w", op, err)
		case apiErr.Code == 429 || driveRateLimited(apiErr):
			return fmt.Errorf("%s: Google Drive rate limit or storage quota reached; wait and retry: %w", op, err)
		case apiErr.Code == 403:
			return fmt.Errorf("%s: Google denied access to the Drive app data folder; check the granted scope and retry login: %w", op, err)
		case apiErr.Code >= 500:
			return fmt.Errorf("%s: Google Drive is temporarily unavailable; retry shortly: %w", op, err)
		}
		return fmt.Errorf("%s: %w", op, err)
	}

	var urlErr *url.Error
	var netErr net.Error
	if errors.As(err, &urlErr) || errors.As(err, &netErr) {
		return fmt.Errorf("%s: could not reach Google Drive; check your network connection and retry: %w", op, err)
	}

	return fmt.Errorf("%s: %w", op, err)
}

func driveRateLimited(apiErr *googleapi.Error) bool {
	for _, item := range apiErr.Errors {
		switch item.Reason {
		case "rateLimitExceeded", "userRateLimitExceeded", "quotaExceeded", "dailyLimitExceeded", "storageQuotaExceeded":
			return true
		}
	}
	return false
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
		return "", classifyDriveError("find "+name, err)
	}
	if len(res.Files) == 0 {
		return "", nil
	}
	return res.Files[0].Id, nil
}
