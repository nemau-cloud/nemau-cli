package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// copyStub answers the copy route with response, and records each request's
// path and decoded body.
func copyStub(t *testing.T, status int, response string) (*httptest.Server, *[]string, *[]map[string]any) {
	t.Helper()
	var paths []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/copy") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, &paths, &bodies
}

const copiedBody = `{"container":"archive","key":"2026/holiday.jpg","size":2048,"etag":"e",
	"last_modified":"2026-09-23T10:00:00Z"}`

func TestObjectCopySendsSourceAndDestination(t *testing.T) {
	isolateConfig(t)
	srv, paths, bodies := copyStub(t, http.StatusCreated, copiedBody)

	out, _, err := run(t, srv, "storage", "object", "copy", "photos", "2026/holiday.jpg", "archive")
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if (*paths)[0] != "/api/v1/storage/containers/photos/copy" {
		t.Errorf("path = %q", (*paths)[0])
	}
	sent := (*bodies)[0]
	want := map[string]any{"key": "2026/holiday.jpg", "destination_container": "archive"}
	if len(sent) != len(want) {
		t.Errorf("body = %v, want exactly %v", sent, want)
	}
	for key, value := range want {
		if sent[key] != value {
			t.Errorf("body[%s] = %v, want %v", key, sent[key], value)
		}
	}
	for _, expected := range []string{"archive", "2026/holiday.jpg", "2.0 KiB"} {
		if !strings.Contains(out, expected) {
			t.Errorf("output lacks %q:\n%s", expected, out)
		}
	}
	if strings.Contains(out, "LOCKED UNTIL") {
		t.Errorf("a lock column for an unlocked copy:\n%s", out)
	}
}

func TestObjectCopyWithDestinationKeyAndMetadata(t *testing.T) {
	isolateConfig(t)
	srv, _, bodies := copyStub(t, http.StatusCreated, copiedBody)

	_, _, err := run(t, srv, "storage", "object", "copy", "photos", "a.jpg", "photos", "b.jpg",
		"--replace-metadata", "--metadata", "owner=ops", "--metadata", "note=a=b,c", "--content-type", "image/jpeg")
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	sent := (*bodies)[0]
	if sent["destination_key"] != "b.jpg" || sent["replace_metadata"] != true || sent["content_type"] != "image/jpeg" {
		t.Errorf("body = %v", sent)
	}
	meta, _ := sent["metadata"].(map[string]any)
	if meta["owner"] != "ops" || meta["note"] != "a=b,c" {
		t.Errorf("metadata = %v", sent["metadata"])
	}
}

// Replacing metadata drops whatever is not given again, so it is never implied
// by --metadata or --content-type alone.
func TestObjectCopyMetadataRequiresReplaceMetadata(t *testing.T) {
	isolateConfig(t)
	srv, paths, _ := copyStub(t, http.StatusCreated, copiedBody)

	for _, args := range [][]string{
		{"--metadata", "owner=ops"},
		{"--content-type", "text/plain"},
	} {
		_, _, err := run(t, srv, append([]string{"storage", "object", "copy", "photos", "a.jpg", "archive"}, args...)...)
		if err == nil || !strings.Contains(err.Error(), "--replace-metadata") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	_, _, err := run(t, srv, "storage", "object", "copy", "photos", "a.jpg", "archive", "--replace-metadata", "--metadata", "novalue")
	if err == nil || !strings.Contains(err.Error(), "key=value") {
		t.Errorf("malformed pair: err = %v", err)
	}
	if len(*paths) != 0 {
		t.Error("no request should have been sent")
	}
}

func TestObjectCopyShowsTheInheritedLock(t *testing.T) {
	isolateConfig(t)
	srv, _, _ := copyStub(t, http.StatusCreated, `{"container":"vault","key":"a.jpg","size":1,"etag":"e",
		"last_modified":"2026-09-23T10:00:00Z",
		"retention":{"mode":"COMPLIANCE","retain_until":"2026-10-23T10:00:01Z"}}`)

	out, _, err := run(t, srv, "storage", "object", "copy", "photos", "a.jpg", "vault")
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if !strings.Contains(out, "LOCKED UNTIL") || !strings.Contains(out, "2026-10-23T10:00:01Z (COMPLIANCE)") {
		t.Errorf("lock not shown:\n%s", out)
	}
}

// --quiet prints the destination key alone, for a script that chains on it.
func TestObjectCopyQuietPrintsTheKey(t *testing.T) {
	isolateConfig(t)
	srv, _, _ := copyStub(t, http.StatusCreated, copiedBody)

	out, _, err := run(t, srv, "-q", "storage", "object", "copy", "photos", "2026/holiday.jpg", "archive")
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if strings.TrimSpace(out) != "2026/holiday.jpg" {
		t.Errorf("quiet output = %q", out)
	}
}

// The API's refusal reaches the user unchanged. For an object over the copy
// limit, the message says what to use instead.
func TestObjectCopyReportsTheRefusal(t *testing.T) {
	isolateConfig(t)
	srv, _, _ := copyStub(t, http.StatusRequestEntityTooLarge,
		`{"error":"object exceeds the 5 GiB a server-side copy accepts in one request; copy it with the project's S3 credentials (rclone copy, aws s3 cp)"}`)

	_, _, err := run(t, srv, "storage", "object", "copy", "isos", "big.iso", "archive")
	if err == nil || !strings.Contains(err.Error(), "rclone") {
		t.Fatalf("err = %v", err)
	}
}

func TestObjectCopyNeedsThreeArguments(t *testing.T) {
	isolateConfig(t)
	srv, paths, _ := copyStub(t, http.StatusCreated, copiedBody)
	if _, _, err := run(t, srv, "storage", "object", "copy", "photos", "a.jpg"); err == nil {
		t.Fatal("copy without a destination accepted")
	}
	if len(*paths) != 0 {
		t.Error("no request should have been sent")
	}
}
