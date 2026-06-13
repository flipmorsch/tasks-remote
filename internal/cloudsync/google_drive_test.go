package cloudsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

func TestClassifyDriveError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"expired auth", &googleapi.Error{Code: 401}, "reauthorize"},
		{"rate limited code", &googleapi.Error{Code: 429}, "rate limit"},
		{"quota reason", &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "storageQuotaExceeded"}}}, "rate limit or storage quota"},
		{"forbidden scope", &googleapi.Error{Code: 403}, "denied access"},
		{"server error", &googleapi.Error{Code: 503}, "temporarily unavailable"},
		{"revoked grant", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}, "no longer valid"},
		{"network", &url.Error{Op: "Post", URL: "https://drive", Err: fmt.Errorf("dial tcp: connection refused")}, "could not reach"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyDriveError("sync", tc.err)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("expected nil error, got %v", got)
				}
				return
			}
			if got == nil || !strings.Contains(got.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", got, tc.want)
			}
		})
	}
}

func TestGoogleDriveClientPutCreateUpdateAndGet(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{}
	contents := map[string][]byte{}
	nextID := 1

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/files":
			name := r.URL.Query().Get("q")
			var found []*drive.File
			for fileName, id := range files {
				if name == "name = '"+fileName+"' and trashed = false" {
					found = append(found, &drive.File{Id: id, Name: fileName})
				}
			}
			writeJSON(t, w, map[string]any{"files": found})
		case r.Method == http.MethodPost && (r.URL.Path == "/upload/files" || r.URL.Path == "/upload/drive/v3/files"):
			id := "file-1"
			if nextID > 1 {
				id = "file-extra"
			}
			nextID++
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read create body: %v", err)
			}
			files[ManifestName] = id
			contents[id] = body
			writeJSON(t, w, map[string]any{"id": id})
		case r.Method == http.MethodPatch && (r.URL.Path == "/upload/files/file-1" || r.URL.Path == "/upload/drive/v3/files/file-1"):
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read update body: %v", err)
			}
			contents["file-1"] = body
			writeJSON(t, w, map[string]any{"id": "file-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/files/file-1" && r.URL.Query().Get("alt") == "media":
			w.Write(contents["file-1"])
		default:
			t.Fatalf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer server.Close()

	service, err := drive.NewService(ctx, option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	client := GoogleDriveClient{Service: service}
	if err := client.Put(ctx, ManifestName, []byte("first")); err != nil {
		t.Fatalf("put create: %v", err)
	}
	if err := client.Put(ctx, ManifestName, []byte("second")); err != nil {
		t.Fatalf("put update: %v", err)
	}
	data, err := client.Get(ctx, ManifestName)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Contains(data, []byte("second")) {
		t.Fatalf("data = %q, want it to contain second", string(data))
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
