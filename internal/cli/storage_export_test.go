package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// objectStore stands in for the S3 store: it serves object bodies at presigned
// URLs and records whether any credential reached it.
type objectStore struct {
	server      *httptest.Server
	objects     map[string]string
	sawKey      bool
	sawAuthzHdr bool
}

func newObjectStore(t *testing.T, objects map[string]string) *objectStore {
	t.Helper()
	store := &objectStore{objects: objects}
	store.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Access-Key-Id") != "" {
			store.sawKey = true
		}
		if r.Header.Get("Authorization") != "" {
			store.sawAuthzHdr = true
		}
		key := strings.TrimPrefix(r.URL.Path, "/store/")
		body, ok := store.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(store.server.Close)
	return store
}

// exportAPI serves the listing and the presign endpoint, pointing presigned
// URLs at the object store.
func exportAPI(t *testing.T, store *objectStore, pageSize int) *httptest.Server {
	t.Helper()
	keys := make([]string, 0, len(store.objects))
	for key := range store.objects {
		keys = append(keys, key)
	}
	// Stable order so the marker paging below is deterministic.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/objects") && r.Method == http.MethodGet:
			marker := r.URL.Query().Get("marker")
			start := 0
			if marker != "" {
				for i, key := range keys {
					if key == marker {
						start = i + 1
						break
					}
				}
			}
			end := start + pageSize
			if end > len(keys) {
				end = len(keys)
			}
			type object struct {
				Name  string `json:"name"`
				Bytes int64  `json:"bytes"`
			}
			page := struct {
				Objects    []object `json:"objects"`
				NextMarker string   `json:"next_marker"`
			}{}
			for _, key := range keys[start:end] {
				page.Objects = append(page.Objects, object{Name: key, Bytes: int64(len(store.objects[key]))})
			}
			if end < len(keys) {
				page.NextMarker = keys[end-1]
			}
			_ = json.NewEncoder(w).Encode(page)

		case strings.HasSuffix(r.URL.Path, "/presign") && r.Method == http.MethodPost:
			var body struct {
				Key string `json:"key"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"url":        store.server.URL + "/store/" + body.Key + "?X-Amz-Signature=abc",
				"method":     "GET",
				"expires_at": "2099-01-01T00:00:00Z",
			})

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"error":"no stub for %s %s"}`, r.Method, r.URL.Path)))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestExportDownloadsEveryPageAndKeepsTheHierarchy(t *testing.T) {
	isolateConfig(t)
	store := newObjectStore(t, map[string]string{
		"root.txt":           "a",
		"folder/file.txt":    "bb",
		"folder/sub/two.bin": "ccc",
		"zzz-last.txt":       "dddd",
	})
	// pageSize 2 forces the listing to paginate, so the test fails if the
	// export stops at the first page.
	srv := exportAPI(t, store, 2)
	dir := t.TempDir()

	_, stderr, err := run(t, srv, "storage", "export", "photos", dir)
	if err != nil {
		t.Fatalf("export: %v (%s)", err, stderr)
	}

	for key, content := range store.objects {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(key)))
		if err != nil {
			t.Errorf("%s not written: %v", key, err)
			continue
		}
		if string(raw) != content {
			t.Errorf("%s = %q, want %q", key, raw, content)
		}
	}
	if !strings.Contains(stderr, "4 downloaded") {
		t.Errorf("summary = %q", stderr)
	}
}

func TestExportSendsNoCredentialToTheObjectStore(t *testing.T) {
	// A presigned URL is already authorized; attaching the access key would
	// hand it to a host that is not the API.
	isolateConfig(t)
	store := newObjectStore(t, map[string]string{"a.txt": "content"})
	srv := exportAPI(t, store, 10)

	if _, _, err := run(t, srv, "storage", "export", "photos", t.TempDir()); err != nil {
		t.Fatalf("export: %v", err)
	}
	if store.sawKey || store.sawAuthzHdr {
		t.Error("a credential was sent to the object store")
	}
}

func TestExportIsResumable(t *testing.T) {
	isolateConfig(t)
	store := newObjectStore(t, map[string]string{"a.txt": "aaa", "b.txt": "bbbb"})
	srv := exportAPI(t, store, 10)
	dir := t.TempDir()

	if _, _, err := run(t, srv, "storage", "export", "photos", dir); err != nil {
		t.Fatalf("first export: %v", err)
	}
	_, stderr, err := run(t, srv, "storage", "export", "photos", dir)
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	// A file of the same size on disk is taken as already downloaded, so an
	// interrupted export resumes instead of starting over.
	if !strings.Contains(stderr, "2 skipped") {
		t.Errorf("the second pass did not resume: %q", stderr)
	}
}

func TestExportReportsFailuresAndExitsNonZero(t *testing.T) {
	isolateConfig(t)
	// The listing announces an object the store does not have.
	store := newObjectStore(t, map[string]string{"present.txt": "ok"})
	store.objects["ghost.txt"] = "" // listed…
	srv := exportAPI(t, store, 10)
	delete(store.objects, "ghost.txt")

	_, stderr, err := run(t, srv, "storage", "export", "photos", t.TempDir())
	if err == nil {
		t.Fatal("an incomplete export must exit non-zero")
	}
	if !strings.Contains(stderr, "failed") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestSafeJoinRefusesEscapingKeys(t *testing.T) {
	// Object keys are arbitrary strings chosen by whoever uploaded them, and
	// this command writes them to disk: no key may lead to a write outside the
	// destination.
	destination := t.TempDir()

	for _, key := range []string{
		"../evade.txt",
		"../../etc/passwd",
		"folder/../../evade.txt",
		"/etc/passwd/../../../evade.txt",
	} {
		if got, err := safeJoin(destination, key); err == nil {
			t.Errorf("safeJoin(%q) = %q, must be refused", key, got)
		}
	}

	// A key starting with a slash is valid in S3; it is anchored inside the
	// destination.
	got, err := safeJoin(destination, "/folder/file.txt")
	if err != nil {
		t.Fatalf("legitimate absolute key refused: %v", err)
	}
	if want := filepath.Join(destination, "folder", "file.txt"); got != want {
		t.Errorf("safeJoin = %q, want %q", got, want)
	}
}

func TestPresignQuietPrintsOnlyTheURL(t *testing.T) {
	// The documented pipeline is: curl -o file "$(nemau ... presign -q)".
	isolateConfig(t)
	store := newObjectStore(t, map[string]string{"a.txt": "x"})
	srv := exportAPI(t, store, 10)

	out, _, err := run(t, srv, "-q", "storage", "object", "presign", "photos", "a.txt")
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	if !strings.HasPrefix(out, store.server.URL) || strings.Count(out, "\n") != 1 {
		t.Errorf("output = %q, want the URL alone", out)
	}
}
