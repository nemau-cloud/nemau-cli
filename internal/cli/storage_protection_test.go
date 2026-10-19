package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func protectionStub(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/storage/containers":
			raw := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(raw)
			bodies = append(bodies, string(raw))
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"task_id":"t","status":"pending"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks/t":
			_, _ = w.Write([]byte(`{"id":"t","status":"completed","resourceId":"c-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/storage/containers":
			_, _ = w.Write([]byte(`{"containers":[
				{"id":"c-1","name":"backups","count":3,"bytes":1024,
				 "s3_bucket":"nemau-dev-22222222222222222222222222222222",
				 "protection":{"versioning":"Enabled","object_lock":true,
				               "retention_mode":"COMPLIANCE","retention_days":30}},
				{"id":"c-2","name":"assets","count":10,"bytes":2048}]}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/protection"):
			_, _ = w.Write([]byte(`{"container":"backups",
				"protection":{"versioning":"Enabled","object_lock":true,
				              "retention_mode":"COMPLIANCE","retention_days":30},
				"immutable":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no stub"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func TestCreateContainerSendsProtectionOptions(t *testing.T) {
	isolateConfig(t)
	srv, bodies := protectionStub(t)

	_, _, err := run(t, srv, "storage", "container", "create", "backups",
		"--object-lock", "--retention-mode", "COMPLIANCE", "--retention-days", "30")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte((*bodies)[0]), &sent); err != nil {
		t.Fatalf("unreadable body %q: %v", (*bodies)[0], err)
	}
	if sent["object_lock"] != true {
		t.Errorf("object_lock not sent: %v", sent)
	}
	if sent["retention_mode"] != "COMPLIANCE" || sent["retention_days"] != float64(30) {
		t.Errorf("retention not sent: %v", sent)
	}
}

func TestComplianceModeWarnsAboutIrreversibility(t *testing.T) {
	// COMPLIANCE mode cannot be undone, even by the platform, so the CLI warns
	// once at creation.
	isolateConfig(t)
	srv, _ := protectionStub(t)

	_, stderr, err := run(t, srv, "storage", "container", "create", "backups",
		"--object-lock", "--retention-mode", "COMPLIANCE", "--retention-days", "30")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(stderr, "COMPLIANCE") || !strings.Contains(stderr, "30") {
		t.Errorf("warning missing or incomplete: %q", stderr)
	}
}

func TestRetentionWithoutObjectLockIsRejectedClientSide(t *testing.T) {
	// Caught before the request is sent, so the user gets the error at once.
	isolateConfig(t)
	srv, bodies := protectionStub(t)

	_, _, err := run(t, srv, "storage", "container", "create", "x", "--retention-days", "30")
	if err == nil || !strings.Contains(err.Error(), "object_lock") {
		t.Fatalf("err = %v", err)
	}
	if len(*bodies) != 0 {
		t.Error("no request should have been sent")
	}
}

func TestUnknownRetentionModeIsRejected(t *testing.T) {
	isolateConfig(t)
	srv, _ := protectionStub(t)

	_, _, err := run(t, srv, "storage", "container", "create", "x",
		"--object-lock", "--retention-mode", "WORM")
	if err == nil || !strings.Contains(err.Error(), "GOVERNANCE") {
		t.Fatalf("err = %v", err)
	}
}

func TestListingShowsWhichContainersAreImmutable(t *testing.T) {
	isolateConfig(t)
	srv, _ := protectionStub(t)

	stdout, _, err := run(t, srv, "storage", "container", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, line := range strings.Split(stdout, "\n") {
		switch {
		case strings.Contains(line, "backups") && !strings.Contains(line, "immutable"):
			t.Errorf("the protected container is not flagged: %q", line)
		case strings.Contains(line, "assets") && strings.Contains(line, "immutable"):
			t.Errorf("a plain container is announced as immutable: %q", line)
		}
	}
}

// The listing shows each container's S3 bucket: the name S3 tools need.
func TestListingShowsTheS3Bucket(t *testing.T) {
	isolateConfig(t)
	srv, _ := protectionStub(t)

	stdout, _, err := run(t, srv, "storage", "container", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(stdout, "S3 BUCKET") {
		t.Errorf("no S3 BUCKET column:\n%s", stdout)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "backups") && !strings.Contains(line, "nemau-dev-22222222222222222222222222222222") {
			t.Errorf("backups without its bucket: %q", line)
		}
	}
}

func TestProtectionCommandReadsTheStore(t *testing.T) {
	isolateConfig(t)
	srv, _ := protectionStub(t)

	stdout, _, err := run(t, srv, "storage", "container", "protection", "backups")
	if err != nil {
		t.Fatalf("protection: %v", err)
	}
	for _, want := range []string{"Enabled", "COMPLIANCE", "30 day(s)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output without %q:\n%s", want, stdout)
		}
	}
	// The output must say whether the container is immutable; versioning alone
	// is not immutability.
	if !strings.Contains(stdout, "immutable") || !strings.Contains(stdout, "yes") {
		t.Errorf("the immutability verdict is missing:\n%s", stdout)
	}
}

func TestVersioningWithoutLockIsNotCalledImmutable(t *testing.T) {
	// Without object lock a delete still destroys a version, so the container
	// must not be reported as immutable.
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"container":"c","protection":{"versioning":"Enabled","object_lock":false},"immutable":false}`))
	}))
	defer srv.Close()

	stdout, _, err := run(t, srv, "storage", "container", "protection", "c")
	if err != nil {
		t.Fatalf("protection: %v", err)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "immutable") && strings.Contains(line, "yes") {
			t.Errorf("versioning alone announced as immutable: %q", line)
		}
	}
}
