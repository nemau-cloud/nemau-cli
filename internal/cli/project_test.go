package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateConfig points the config file at a temporary directory so a test never
// reads or writes the developer's real ~/.config/nemau/config.json.
func isolateConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("NEMAU_PROJECT_ID", "")
	return filepath.Join(dir, "nemau", "config.json")
}

const projectsBody = `{"projects":[
	{"id":"11111111-1111-1111-1111-111111111111","name":"staging","slug":"staging",
	 "region":"fr-occitanie-1","is_active":true,"provisioning_status":"active","current":false},
	{"id":"22222222-2222-2222-2222-222222222222","name":"prod","slug":"prod",
	 "region":"fr-occitanie-1","is_active":true,"provisioning_status":"active","current":true}
],"count":2,"current_project_id":"22222222-2222-2222-2222-222222222222"}`

const (
	stagingID = "11111111-1111-1111-1111-111111111111"
	prodID    = "22222222-2222-2222-2222-222222222222"
)

// projectStub answers the listing and records the project header of every call.
func projectStub(t *testing.T, extra map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var headers []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = append(headers, r.Header.Get("X-Project-Id"))
		if body, ok := extra[r.Method+" "+r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if r.URL.Path == "/api/v1/orchestration/projects" {
			_, _ = w.Write([]byte(projectsBody))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no stub for ` + r.URL.Path + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &headers
}

func TestProjectListMarksTheCurrentOne(t *testing.T) {
	isolateConfig(t)
	srv, _ := projectStub(t, nil)

	out, _, err := run(t, srv, "project", "list")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"staging", "prod", "fr-occitanie-1", "ready"} {
		if !strings.Contains(out, want) {
			t.Errorf("output without %q:\n%s", want, out)
		}
	}
	// The asterisk marks the project a command would use now. It is checked per
	// line rather than on the raw string, so the test does not depend on the
	// column padding.
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, prodID) && !strings.HasPrefix(line, "*"):
			t.Errorf("the current project is not marked: %q", line)
		case strings.Contains(line, stagingID) && strings.HasPrefix(line, "*"):
			t.Errorf("a non-current project is marked: %q", line)
		}
	}
}

func TestProjectUseResolvesNameAndStoresTheID(t *testing.T) {
	path := isolateConfig(t)
	srv, _ := projectStub(t, nil)

	if _, _, err := run(t, srv, "project", "use", "staging"); err != nil {
		t.Fatalf("run: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	var stored struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("config is unreadable: %v", err)
	}
	// The id is stored rather than the name, so a renamed project keeps working
	// and later commands do not resolve it again.
	if stored.ProjectID != stagingID {
		t.Errorf("config contains %q, want %q", stored.ProjectID, stagingID)
	}
}

func TestStoredProjectIsSentAsAHeader(t *testing.T) {
	isolateConfig(t)
	srv, headers := projectStub(t, map[string]string{
		"GET /api/v1/compute/instances": `{"instances":[]}`,
	})

	if _, _, err := run(t, srv, "project", "use", "staging"); err != nil {
		t.Fatalf("project use: %v", err)
	}
	if _, _, err := run(t, srv, "compute", "instance", "list"); err != nil {
		t.Fatalf("instance list: %v", err)
	}

	last := (*headers)[len(*headers)-1]
	if last != stagingID {
		t.Errorf("X-Project-Id = %q, want %q", last, stagingID)
	}
}

func TestProjectPrecedence(t *testing.T) {
	// The flag wins over the environment, which wins over the file: the order
	// gcloud and scw use.
	isolateConfig(t)
	srv, headers := projectStub(t, map[string]string{
		"GET /api/v1/compute/instances": `{"instances":[]}`,
	})

	if _, _, err := run(t, srv, "project", "use", "staging"); err != nil {
		t.Fatalf("project use: %v", err)
	}

	t.Run("the file applies by default", func(t *testing.T) {
		*headers = nil
		if _, _, err := run(t, srv, "compute", "instance", "list"); err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := (*headers)[0]; got != stagingID {
			t.Errorf("header = %q, want %q", got, stagingID)
		}
	})

	t.Run("the environment variable wins over the file", func(t *testing.T) {
		t.Setenv("NEMAU_PROJECT_ID", prodID)
		*headers = nil
		if _, _, err := run(t, srv, "compute", "instance", "list"); err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := (*headers)[0]; got != prodID {
			t.Errorf("header = %q, want %q", got, prodID)
		}
	})

	t.Run("the flag wins over everything", func(t *testing.T) {
		t.Setenv("NEMAU_PROJECT_ID", prodID)
		*headers = nil
		if _, _, err := run(t, srv, "--project", stagingID, "compute", "instance", "list"); err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := (*headers)[0]; got != stagingID {
			t.Errorf("header = %q, want %q", got, stagingID)
		}
	})
}

func TestNoSelectionSendsNoHeader(t *testing.T) {
	// Without a selection the credential's own project applies, so the CLI must
	// not send the header at all: an empty header would select no project.
	isolateConfig(t)
	srv, headers := projectStub(t, map[string]string{
		"GET /api/v1/compute/instances": `{"instances":[]}`,
	})

	if _, _, err := run(t, srv, "compute", "instance", "list"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := (*headers)[0]; got != "" {
		t.Errorf("header = %q, want none", got)
	}
}

func TestProjectFlagAcceptsANameWithoutExtraCallsForAnID(t *testing.T) {
	isolateConfig(t)
	srv, headers := projectStub(t, map[string]string{
		"GET /api/v1/compute/instances": `{"instances":[]}`,
	})

	// A name has to be resolved: listing first, then the real call.
	*headers = nil
	if _, _, err := run(t, srv, "--project", "prod", "compute", "instance", "list"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(*headers) != 2 {
		t.Fatalf("%d requests for a name, want 2 (resolution + call)", len(*headers))
	}
	if got := (*headers)[1]; got != prodID {
		t.Errorf("header of the call = %q, want %q", got, prodID)
	}

	// An id costs no extra request.
	*headers = nil
	if _, _, err := run(t, srv, "--project", prodID, "compute", "instance", "list"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(*headers) != 1 {
		t.Errorf("%d requests for an id, want 1", len(*headers))
	}
}

func TestUnknownProjectNamePointsAtTheListing(t *testing.T) {
	isolateConfig(t)
	srv, _ := projectStub(t, nil)

	_, _, err := run(t, srv, "--project", "qa", "compute", "instance", "list")
	if err == nil {
		t.Fatal("an unknown project must fail")
	}
	if !strings.Contains(err.Error(), "qa") || !strings.Contains(err.Error(), "project list") {
		t.Errorf("message = %q, must name the project and the listing command", err)
	}
}

func TestProjectUnsetFallsBackToTheCredential(t *testing.T) {
	isolateConfig(t)
	srv, headers := projectStub(t, map[string]string{
		"GET /api/v1/compute/instances": `{"instances":[]}`,
	})

	if _, _, err := run(t, srv, "project", "use", "staging"); err != nil {
		t.Fatalf("project use: %v", err)
	}
	if _, _, err := run(t, srv, "project", "unset"); err != nil {
		t.Fatalf("project unset: %v", err)
	}

	*headers = nil
	if _, _, err := run(t, srv, "compute", "instance", "list"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := (*headers)[0]; got != "" {
		t.Errorf("header = %q after unset, want none", got)
	}
}

func TestConfigFileIsNotWorldReadable(t *testing.T) {
	// The file holds no secret, but it is still readable by its owner only.
	path := isolateConfig(t)
	srv, _ := projectStub(t, nil)

	if _, _, err := run(t, srv, "project", "use", "prod"); err != nil {
		t.Fatalf("run: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %04o, want 0600", mode)
	}
}

func TestCorruptConfigIsIgnored(t *testing.T) {
	path := isolateConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv, headers := projectStub(t, map[string]string{
		"GET /api/v1/compute/instances": `{"instances":[]}`,
	})
	if _, _, err := run(t, srv, "compute", "instance", "list"); err != nil {
		t.Fatalf("a corrupt file must be ignored: %v", err)
	}
	if got := (*headers)[0]; got != "" {
		t.Errorf("header = %q, want none", got)
	}
}
