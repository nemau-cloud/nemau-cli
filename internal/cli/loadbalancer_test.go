package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadBalancerFlavorsListsTiersWithTheirPrice(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/network/load-balancer-flavors": `{"flavors":[
			{"name":"lb-small","appliances":1,"vcpus":1,"ram_mb":1024,"high_availability":false,"monthly_price":5.4,"currency":"eur"},
			{"name":"lb-large","appliances":2,"vcpus":4,"ram_mb":4096,"high_availability":true,"monthly_price":32.4,"currency":"eur"}
		],"default":"lb-small"}`,
	})
	out, _, err := run(t, srv, "lb", "flavors")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"lb-small", "5.40 EUR/month", "lb-large", "32.40 EUR/month", "4096 MB", "yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestLoadBalancerCreateSendsTheFlavor(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/network/load-balancers" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"task_id":"t","status":"pending"}`))
	}))
	t.Cleanup(srv.Close)

	if _, _, err := run(t, srv, "--wait=false", "lb", "instance", "create", "edge", "--vip-subnet", "sub-1", "--flavor", "lb-ha"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if body["flavor"] != "lb-ha" || body["vip_subnet_id"] != "sub-1" {
		t.Fatalf("create body = %v", body)
	}
	if _, sent := body["provider"]; sent {
		t.Fatalf("the create body carries an engine: %v", body)
	}
}

// There is no engine to choose: the API applies automatic mode, and the CLI has
// no flag for it.
func TestLoadBalancerCreateHasNoEngineFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no request expected, got %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, _, err := run(t, srv, "--wait=false", "lb", "instance", "create", "edge", "--vip-subnet", "sub-1", "--provider", "haproxy")
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --provider") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadBalancerGetShowsTheTierAndWhichApplianceIsActive(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/network/load-balancers/lb-1": `{"load_balancer":{"id":"lb-1","name":"edge","flavor":"lb-medium",
			"provider":"auto","provisioning_status":"ACTIVE","operating_status":"ONLINE","admin_state_up":true,
			"listeners":[],"pools":[],
			"high_availability":{"status":"DEGRADED","reason":"SINGLE_HOST","appliances":[
				{"index":1,"state":"STANDBY","address":"10.0.0.11"},{"index":2,"state":"ACTIVE","address":"10.0.0.12"}]}}}`,
	})
	out, _, err := run(t, srv, "lb", "instance", "get", "lb-1")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"lb-medium", "auto", "DEGRADED (SINGLE_HOST)", "STANDBY, 10.0.0.11", "ACTIVE, 10.0.0.12"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestLoadBalancerListShowsTierAndHA(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/network/load-balancers": `{"load_balancers":[
			{"id":"lb-1","name":"small","flavor":"lb-small","listeners":[],"pools":[]},
			{"id":"lb-2","name":"pair","flavor":"lb-ha","listeners":[],"pools":[],
			 "high_availability":{"status":"REDUNDANT","appliances":[{"index":1,"state":"ACTIVE"},{"index":2,"state":"STANDBY"}]}}]}`,
	})
	out, _, err := run(t, srv, "lb", "instance", "list")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"FLAVOR", "HA", "lb-small", "lb-ha", "REDUNDANT"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}
