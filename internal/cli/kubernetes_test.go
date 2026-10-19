package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The listing is the platform's summary row: a lowercase status, with the
// version and phase in specs. It is not decoded as the detail type.
func TestClusterListReadsTheSummary(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/kubernetes/clusters": `{"clusters":[
			{"id":"k-1","name":"prod","status":"active","specs":{"phase":"Provisioned",
			 "kubernetes_version":"v1.35.0","control_plane_ready":true,"control_plane_replicas":3,
			 "node_pools_count":2},"createdAt":"2026-09-01T10:00:00Z"},
			{"id":"k-2","name":"staging","status":"building","specs":{}}],"count":2}`,
	})

	out, _, err := run(t, srv, "k8s", "cluster", "list")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"prod", "active", "v1.35.0", "Provisioned", "3 ready", "staging", "building"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// `upgrade` is one call: the platform runs the preflight, the maintenance window
// and the upgrade inside the task. The body is exactly {"version": ...}.
func TestClusterUpgradeIsOneCallWithVersion(t *testing.T) {
	var calls []string
	var upgradeBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &upgradeBody)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"task-up","status":"queued"}`))
	}))
	t.Cleanup(srv.Close)

	out, _, err := run(t, srv, "--wait=false", "kubernetes", "cluster", "upgrade", "prod", "--version", "1.36.0")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(calls) != 1 || calls[0] != "POST /api/v1/kubernetes/clusters/prod/upgrade" {
		t.Errorf("calls = %v", calls)
	}
	if len(upgradeBody) != 1 || upgradeBody["version"] != "1.36.0" {
		t.Errorf("upgrade body = %v", upgradeBody)
	}
	if !strings.Contains(out, "task-up") {
		t.Errorf("the task is not reported:\n%s", out)
	}
}

func TestClusterUpgradeRefusesABadVersionLocally(t *testing.T) {
	srv := stub(t, map[string]string{})
	if _, _, err := run(t, srv, "k8s", "cluster", "upgrade", "prod", "--version", "latest"); err == nil {
		t.Fatal("a malformed version was accepted")
	}
	if _, _, err := run(t, srv, "k8s", "cluster", "upgrade", "prod"); err == nil {
		t.Fatal("--version is required")
	}
}

// createServer records the body of POST /kubernetes/clusters (or of an added
// node pool) and answers with a queued task.
func createServer(t *testing.T, path string) (*httptest.Server, *map[string]any) {
	t.Helper()
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != path {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"task-1","status":"queued"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

// Without --node-pool and --control-plane-replicas, the request carries the
// console's defaults: the API refuses a cluster without them.
func TestClusterCreateSendsTheConsoleDefaults(t *testing.T) {
	srv, body := createServer(t, "/api/v1/kubernetes/clusters")
	_, _, err := run(t, srv, "--wait=false", "k8s", "cluster", "create", "prod", "--version", "v1.31.0",
		"--control-plane-vcpus", "2", "--control-plane-ram", "4096", "--control-plane-disk", "20")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if (*body)["control_plane_replicas"] != float64(3) {
		t.Errorf("control_plane_replicas = %v, want 3", (*body)["control_plane_replicas"])
	}
	pools, _ := (*body)["node_pools"].([]any)
	if len(pools) != 1 {
		t.Fatalf("node_pools = %v, want the default pool", (*body)["node_pools"])
	}
	want := map[string]any{"name": "workers", "class": "default-worker", "replicas": float64(3),
		"vcpus": float64(2), "ram_mb": float64(4096), "disk_gb": float64(20)}
	got := pools[0].(map[string]any)
	for key, value := range want {
		if got[key] != value {
			t.Errorf("default pool %s = %v, want %v", key, got[key], value)
		}
	}
}

func TestClusterCreateSendsTheDeclaredPools(t *testing.T) {
	srv, body := createServer(t, "/api/v1/kubernetes/clusters")
	_, _, err := run(t, srv, "--wait=false", "k8s", "cluster", "create", "prod", "--version", "v1.31.0",
		"--control-plane-flavor", "cp-flavor", "--control-plane-replicas", "5", "--worker-flavor", "w-flavor",
		"--node-pool", "name=workers,replicas=0",
		"--node-pool", "name=batch,class=batch-worker,vcpus=4,ram=8192,disk=50,autoscaler-min=2,autoscaler-max=6",
		"--node-pool", "name=gpu,flavor=gpu-flavor,replicas=1")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if (*body)["control_plane_replicas"] != float64(5) || (*body)["worker_flavor_id"] != "w-flavor" {
		t.Errorf("body = %v", *body)
	}
	pools, _ := (*body)["node_pools"].([]any)
	if len(pools) != 3 {
		t.Fatalf("node_pools = %v", (*body)["node_pools"])
	}
	workers, batch, gpu := pools[0].(map[string]any), pools[1].(map[string]any), pools[2].(map[string]any)
	// Zero nodes is a valid pool, and with --worker-flavor a pool given no size
	// leaves it to the flavor.
	if workers["replicas"] != float64(0) || workers["vcpus"] != nil {
		t.Errorf("workers = %v", workers)
	}
	// An autoscaled pool starts at its floor.
	if batch["replicas"] != float64(2) || batch["class"] != "batch-worker" || batch["vcpus"] != float64(4) ||
		batch["autoscaler_min"] != float64(2) || batch["autoscaler_max"] != float64(6) {
		t.Errorf("batch = %v", batch)
	}
	if gpu["flavor_id"] != "gpu-flavor" || gpu["vcpus"] != nil || gpu["replicas"] != float64(1) {
		t.Errorf("gpu = %v", gpu)
	}
}

// Mistakes the API would refuse are caught before any request: the stub has
// no route, so a request would fail the test.
func TestClusterCreateRejectsInvalidPoolsBeforeTheAPI(t *testing.T) {
	base := []string{"--wait=false", "k8s", "cluster", "create", "prod", "--version", "v1.31.0",
		"--control-plane-vcpus", "2", "--control-plane-ram", "4096", "--control-plane-disk", "20"}
	cases := map[string]struct {
		args []string
		want string
	}{
		"zero control-plane replicas": {[]string{"--control-plane-replicas", "0"}, "--control-plane-replicas"},
		"ten control-plane replicas":  {[]string{"--control-plane-replicas", "10"}, "--control-plane-replicas"},
		"pool without a name":         {[]string{"--node-pool", "replicas=3"}, "DNS label"},
		"pool with an invalid name":   {[]string{"--node-pool", "name=Workers"}, "DNS label"},
		"duplicate pool names":        {[]string{"--node-pool", "name=a", "--node-pool", "name=a"}, "twice"},
		"unknown key":                 {[]string{"--node-pool", "name=a,size=large"}, "unknown key"},
		"partial size":                {[]string{"--node-pool", "name=a,vcpus=2"}, "all three"},
		"flavor and size":             {[]string{"--node-pool", "name=a,flavor=f,vcpus=2,ram=4096,disk=20"}, "not both"},
		"too many replicas":           {[]string{"--node-pool", "name=a,replicas=101"}, "between 0 and 100"},
		"half an autoscaler":          {[]string{"--node-pool", "name=a,autoscaler-min=1"}, "go together"},
		"inverted autoscaler":         {[]string{"--node-pool", "name=a,autoscaler-min=5,autoscaler-max=2"}, "autoscaler"},
		"negative replicas":           {[]string{"--node-pool", "name=a,replicas=-1"}, "non-negative"},
		"missing value":               {[]string{"--node-pool", "name=a,replicas"}, "key=value"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := stub(t, map[string]string{})
			_, _, err := run(t, srv, append(append([]string{}, base...), tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// A pool added with a flavor sends it; one added with a custom size sends no
// flavor_id at all.
func TestNodePoolAddSendsFlavorOrSize(t *testing.T) {
	srv, body := createServer(t, "/api/v1/kubernetes/clusters/prod/node-pools")
	if _, _, err := run(t, srv, "--wait=false", "k8s", "node-pool", "add", "prod", "gpu",
		"--class", "default-worker", "--flavor", "gpu-flavor", "--replicas", "1"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if (*body)["flavor_id"] != "gpu-flavor" || (*body)["pool_name"] != "gpu" || (*body)["class"] != "default-worker" {
		t.Errorf("body = %v", *body)
	}

	srv, body = createServer(t, "/api/v1/kubernetes/clusters/prod/node-pools")
	if _, _, err := run(t, srv, "--wait=false", "k8s", "node-pool", "add", "prod", "batch",
		"--class", "default-worker", "--vcpus", "4", "--ram", "8192", "--disk", "50", "--replicas", "2"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, present := (*body)["flavor_id"]; present {
		t.Errorf("custom-size pool sent a flavor_id: %v", *body)
	}
	if (*body)["vcpus"] != float64(4) || (*body)["replicas"] != float64(2) {
		t.Errorf("body = %v", *body)
	}
}
