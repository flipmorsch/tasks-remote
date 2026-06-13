package cloudsync

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

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
