package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// run executes the command tree against a stub API and returns stdout.
//
// The command tree is rebuilt every time, but `g` is package state, so it is
// reset here: without that, the client cached by one test would answer the
// next one's requests.
func run(t *testing.T, srv *httptest.Server, args ...string) (string, string, error) {
	t.Helper()
	*g = globals{}

	root := newRootCommand("test")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"--api-url", srv.URL}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// stub answers the endpoints a test names and fails the test on any other
// request: an unexpected path means the CLI calls something the API does not
// have.
func stub(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		body, ok := routes[key]
		if !ok {
			t.Errorf("unexpected request %s (query %q)", key, r.URL.RawQuery)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no stub"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestInstanceListTable(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/compute/instances": `{"instances":[
			{"id":"i-1","name":"web-1","status":"running","sizing":{"vcpus":2,"ram_mb":4096,"disk_gb":40},
			 "addresses":["10.0.0.5","10.0.0.6","10.0.0.7"]}]}`,
	})

	out, _, err := run(t, srv, "compute", "instance", "list")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"i-1", "web-1", "running", "4096 MB", "40 GB"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	// Three addresses with room for two: the overflow is counted so that the
	// reader knows the cell is truncated.
	if !strings.Contains(out, "(+1)") {
		t.Errorf("address list was truncated without saying so:\n%s", out)
	}
}

func TestQuietPrintsOnlyIDs(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/network/networks": `{"networks":[{"id":"net-1","name":"a"},{"id":"net-2","name":"b"}]}`,
	})

	out, _, err := run(t, srv, "-q", "network", "vpc", "list")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "net-1\nnet-2\n" {
		t.Errorf("--quiet output = %q", out)
	}
}

func TestJSONOutputIsTheAPIShape(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/storage/volumes": `{"volumes":[{"id":"vol-1","name":"data","size":100,"status":"available","attachments":[]}]}`,
	})

	out, _, err := run(t, srv, "-o", "json", "storage", "volume", "list")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var decoded []struct {
		ID   string `json:"id"`
		Size int    `json:"size"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(decoded) != 1 || decoded[0].ID != "vol-1" || decoded[0].Size != 100 {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestWriteWaitsForTaskByDefault(t *testing.T) {
	srv := stub(t, map[string]string{
		"POST /api/v1/network/networks": `{"task_id":"task-9","status":"pending"}`,
		"GET /api/v1/tasks/task-9":      `{"id":"task-9","taskType":"network:create","status":"completed","progress":100,"resourceId":"net-7"}`,
	})

	out, _, err := run(t, srv, "network", "vpc", "create", "prod")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "net-7") || !strings.Contains(out, "completed") {
		t.Errorf("the wait did not report the created resource:\n%s", out)
	}
}

func TestWriteWithoutWaitReturnsTheTask(t *testing.T) {
	// --wait=false must not poll: the stub fails the test on any unexpected
	// request, so a stray GET /tasks/... would be caught here.
	srv := stub(t, map[string]string{
		"POST /api/v1/network/networks": `{"task_id":"task-9","status":"pending"}`,
	})

	out, _, err := run(t, srv, "--wait=false", "network", "vpc", "create", "prod")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "task-9") {
		t.Errorf("output = %s", out)
	}
}

func TestFailedTaskIsAnError(t *testing.T) {
	srv := stub(t, map[string]string{
		"POST /api/v1/compute/instances": `{"task_id":"t","status":"pending"}`,
		"GET /api/v1/tasks/t": `{"id":"t","taskType":"compute:create","status":"failed",
			"error":"the operation failed; contact support with the task id"}`,
	})

	_, _, err := run(t, srv, "compute", "instance", "create",
		"--name", "x", "--image", "img", "--vcpus", "2", "--ram", "4096", "--disk", "40")
	if err == nil {
		t.Fatal("a failed task must exit non-zero")
	}
	// The platform replaces the underlying error with a fixed message, so the
	// task id is what support needs, and it must reach the user.
	if !strings.Contains(err.Error(), "task t") {
		t.Errorf("error text omits the task id: %v", err)
	}
}

func TestPartialSizingIsRejectedBeforeTheAPI(t *testing.T) {
	// No stub routes, so any request fails the test: the check must happen in
	// the CLI and name the flags the user typed rather than the JSON fields.
	srv := stub(t, map[string]string{})

	_, _, err := run(t, srv, "compute", "instance", "create",
		"--name", "x", "--image", "img", "--vcpus", "2")
	if err == nil || !strings.Contains(err.Error(), "--flavor") {
		t.Fatalf("err = %v, want a message naming the flags", err)
	}
}

func TestSetSecurityGroupsRejectsContradictoryFlags(t *testing.T) {
	srv := stub(t, map[string]string{})
	_, _, err := run(t, srv, "compute", "instance", "set-security-groups", "i-1",
		"--clear", "--security-group", "sg-1")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v", err)
	}
}

func TestAPIErrorReachesTheUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"not authorized for compute:Read","reason":"permission_denied"}`))
	}))
	defer srv.Close()

	_, _, err := run(t, srv, "compute", "instance", "list")
	if err == nil || !strings.Contains(err.Error(), "not authorized for compute:Read") {
		t.Fatalf("err = %v", err)
	}
}

// An overlapping range is a refusal the user can fix: the message must name the
// conflicting subnet and point at the listing of the ranges already in use.
func TestSubnetOverlapNamesTheConflictAndTheListing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path != "POST /api/v1/network/networks/net-1/subnets" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"cidr 10.0.1.0/24 overlaps subnet \"app\" (10.0.0.0/16) on network \"backend\"; the subnets of a project are routed together, so their ranges must not overlap","reason":"subnet_overlap"}`))
	}))
	defer srv.Close()

	_, _, err := run(t, srv, "network", "subnet", "create", "net-1", "web", "--cidr", "10.0.1.0/24")
	if err == nil {
		t.Fatal("an overlapping subnet must fail")
	}
	for _, want := range []string{`overlaps subnet "app" (10.0.0.0/16) on network "backend"`, "nemau network subnet list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, missing %q", err, want)
		}
	}
	// The message carries no transport detail.
	if strings.Contains(err.Error(), "/api/v1/") || strings.Contains(err.Error(), "(subnet_overlap)") {
		t.Errorf("message still carries the transport detail: %q", err)
	}
}

func TestUnknownOutputFormatIsRejected(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/regions": `[{"slug":"fr-occitanie-1","displayName":"Occitanie","country":"FR","enabled":true}]`,
	})
	_, _, err := run(t, srv, "-o", "yaml", "region", "list")
	if err == nil || !strings.Contains(err.Error(), "yaml") {
		t.Fatalf("err = %v", err)
	}
}

func TestEmptyListSaysSoOnStderr(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/dns/zones": `{"zones":[],"count":0}`,
	})
	out, errOut, err := run(t, srv, "dns", "zone", "list")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "" {
		t.Errorf("stdout should stay empty so a pipe gets nothing, got %q", out)
	}
	if !strings.Contains(errOut, "no results") {
		t.Errorf("stderr = %q", errOut)
	}
}
