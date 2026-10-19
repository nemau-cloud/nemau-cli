package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
)

// bodyStub answers like stub but keeps the body of every write, so a test
// can assert what the CLI actually sent.
func bodyStub(t *testing.T, routes map[string]string) (*httptest.Server, map[string]string) {
	t.Helper()
	bodies := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			bodies[key] = string(raw)
		}
		body, ok := routes[key]
		if !ok {
			t.Errorf("unexpected request %s", key)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, bodies
}

func decodeBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", raw, err)
	}
	return body
}

func TestIPv6SubnetIsCreatedWithoutARange(t *testing.T) {
	srv, bodies := bodyStub(t, map[string]string{
		"POST /api/v1/network/networks/net-1/subnets": `{"task_id":"t","status":"pending"}`,
	})
	if _, _, err := run(t, srv, "--wait=false", "network", "subnet", "create", "net-1", "v6", "--ip-version", "6"); err != nil {
		t.Fatalf("run: %v", err)
	}
	body := decodeBody(t, bodies["POST /api/v1/network/networks/net-1/subnets"])
	if body["ip_version"] != float64(6) || body["name"] != "v6" {
		t.Fatalf("body = %v", body)
	}
	if _, present := body["cidr"]; present {
		t.Fatalf("an IPv6 subnet sent a cidr: %v", body)
	}
}

func TestIPv4SubnetBodyIsUnchanged(t *testing.T) {
	srv, bodies := bodyStub(t, map[string]string{
		"POST /api/v1/network/networks/net-1/subnets": `{"task_id":"t","status":"pending"}`,
	})
	if _, _, err := run(t, srv, "--wait=false", "network", "subnet", "create", "net-1", "v4", "--cidr", "10.0.0.0/24"); err != nil {
		t.Fatalf("run: %v", err)
	}
	body := decodeBody(t, bodies["POST /api/v1/network/networks/net-1/subnets"])
	if body["cidr"] != "10.0.0.0/24" {
		t.Fatalf("body = %v", body)
	}
	if _, present := body["ip_version"]; present {
		t.Fatalf("an IPv4 subnet sent an ip_version: %v", body)
	}
}

// Each of these is a mistake the API would also refuse; the CLI names the
// flag instead, before any request (the stub has no route at all).
func TestSubnetFlagsAreCheckedBeforeTheAPI(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"net-1", "v4"}, "--cidr is required"},
		{[]string{"net-1", "v6", "--ip-version", "6", "--cidr", "2001:db8::/64"}, "do not apply to an IPv6 subnet"},
		{[]string{"net-1", "v6", "--ip-version", "6", "--gateway", "2001:db8::1"}, "do not apply to an IPv6 subnet"},
		{[]string{"net-1", "v5", "--ip-version", "5", "--cidr", "10.0.0.0/24"}, "must be 4 or 6"},
	} {
		srv := stub(t, map[string]string{})
		_, _, err := run(t, srv, append([]string{"network", "subnet", "create"}, tc.args...)...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestFloatingIPAllocationCarriesTheVersion(t *testing.T) {
	for _, tc := range []struct {
		args        []string
		wantVersion any
	}{
		{[]string{"--ip-version", "6"}, float64(6)},
		{nil, nil},
	} {
		srv, bodies := bodyStub(t, map[string]string{
			"POST /api/v1/network/floating-ips": `{"task_id":"t","status":"pending"}`,
		})
		args := append([]string{"--wait=false", "network", "floating-ip", "allocate"}, tc.args...)
		if _, _, err := run(t, srv, args...); err != nil {
			t.Fatalf("run: %v", err)
		}
		body := decodeBody(t, bodies["POST /api/v1/network/floating-ips"])
		if body["ip_version"] != tc.wantVersion {
			t.Errorf("%v: body = %v", tc.args, body)
		}
	}
}

func TestListsShowTheIPVersion(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/network/floating-ips": `{"floatingips":[
			{"id":"fip-4","address":"203.0.113.7","status":"available"},
			{"id":"fip-6","address":"2001:db8:f::10","ip_version":6,"status":"attached"}]}`,
		"GET /api/v1/network/subnets": `{"subnets":[
			{"id":"sub-6","name":"v6","network_id":"net-1","cidr":"2001:db8:100:1::/64","ip_version":6}]}`,
	})
	out, _, err := run(t, srv, "network", "floating-ip", "list")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "IPv4") || !strings.Contains(out, "IPv6") {
		t.Errorf("floating IP list without versions:\n%s", out)
	}
	out, _, err = run(t, srv, "network", "subnet", "list")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "IPv6") || !strings.Contains(out, "2001:db8:100:1::/64") {
		t.Errorf("subnet list without its version:\n%s", out)
	}
}

func TestNetworkCapabilities(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/network/capabilities": `{"capabilities":{"ipv6_floating_ips":true,"ipv6_subnets":false}}`,
	})
	out, _, err := run(t, srv, "network", "capabilities")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compact := collapseSpaces(out)
	if !strings.Contains(compact, "IPv6 floating IPs yes") || !strings.Contains(compact, "IPv6 subnets no") {
		t.Errorf("capabilities output:\n%s", out)
	}
}

// An exported IPv6 subnet must not pin its /64: the provider refuses a cidr on
// an IPv6 subnet, and the range is the platform's to assign.
func TestExportDeclaresIPv6WithoutARange(t *testing.T) {
	document := renderInventory(inventory{
		Networks: []nemau.Network{{ID: "net-1", Name: "app"}},
		Subnets: []nemau.Subnet{
			{ID: "sub-6", Name: "app-v6", NetworkID: "net-1", CIDR: "2001:db8:100:1::/64",
				GatewayIP: "2001:db8:100:1::1", IPVersion: nemau.IPv6},
		},
		FloatingIPs: []nemau.FloatingIP{{ID: "fip-6", Address: "2001:db8:f::10", IPVersion: nemau.IPv6}},
	})
	hcl := collapseSpaces(document.render(""))
	for _, want := range []string{"ip_version = 6", "Current range: 2001:db8:100:1::/64"} {
		if !strings.Contains(hcl, want) {
			t.Errorf("HCL without %q:\n%s", want, hcl)
		}
	}
	for _, unwanted := range []string{`cidr = "2001`, `gateway_ip = "2001`} {
		if strings.Contains(hcl, unwanted) {
			t.Errorf("HCL pins the assigned range with %q:\n%s", unwanted, hcl)
		}
	}
	if strings.Count(hcl, "ip_version = 6") != 2 {
		t.Errorf("both the subnet and the floating IP should declare IPv6:\n%s", hcl)
	}
}

const egressGroups = `{"security_groups":[
	{"id":"sg-old","name":"legacy","egress_filtering":false,"rules":[
		{"id":"r1","security_group_id":"sg-old","direction":"egress","action":"accept","remote_ip_prefix":"198.51.100.0/24"}]},
	{"id":"sg-new","name":"web","egress_filtering":true,"rules":[
		{"id":"r2","security_group_id":"sg-new","direction":"egress","action":"accept","remote_ip_prefix":"0.0.0.0/0"},
		{"id":"r3","security_group_id":"sg-new","direction":"egress","action":"accept","remote_ip_prefix":"::/0"},
		{"id":"r4","security_group_id":"sg-new","direction":"ingress","action":"accept","protocol":"tcp","port_range_min":22,"port_range_max":22}]}
],"count":2}`

func TestSecurityGroupListShowsEgressFiltering(t *testing.T) {
	srv := stub(t, map[string]string{"GET /api/v1/network/security-groups": egressGroups})
	out, _, err := run(t, srv, "network", "security-group", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "EGRESS") {
		t.Fatalf("output:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(lines[1]), "not filtered") || !strings.HasSuffix(strings.TrimSpace(lines[2]), "filtered") ||
		strings.HasSuffix(strings.TrimSpace(lines[2]), "not filtered") {
		t.Errorf("egress column is wrong:\n%s", out)
	}
}

func TestSecurityGroupGetExplainsAnOldGroup(t *testing.T) {
	srv := stub(t, map[string]string{"GET /api/v1/network/security-groups": egressGroups})
	out, _, err := run(t, srv, "network", "security-group", "get", "sg-old")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"not filtered", "egress rules have no effect", "enable-egress-filtering"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}

	out, _, err = run(t, srv, "network", "security-group", "get", "sg-new")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no effect") || !strings.Contains(out, "filtered") {
		t.Errorf("a filtering group must not carry the note:\n%s", out)
	}
}

func TestSecurityGroupRulesWarnsWhenEgressRulesAreInert(t *testing.T) {
	srv := stub(t, map[string]string{"GET /api/v1/network/security-groups": egressGroups})
	out, stderr, err := run(t, srv, "network", "security-group", "rules", "sg-old")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "198.51.100.0/24") || !strings.Contains(stderr, "no effect") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", out, stderr)
	}
	_, stderr, err = run(t, srv, "network", "security-group", "rules", "sg-new")
	if err != nil {
		t.Fatal(err)
	}
	if stderr != "" {
		t.Errorf("a filtering group must not warn, got %q", stderr)
	}
}

func TestEnableEgressFilteringSubmitsAndWaits(t *testing.T) {
	srv, bodies := bodyStub(t, map[string]string{
		"POST /api/v1/network/security-groups/sg-old/enable-egress-filtering": `{"task_id":"t-1","status":"pending"}`,
		"GET /api/v1/tasks/t-1": `{"id":"t-1","taskType":"network:enable_security_group_egress_filtering","status":"completed",` +
			`"progress":100,"resourceId":"sg-old","result":{"instances_updated":2,"instances_filtering_egress":1}}`,
	})
	out, _, err := run(t, srv, "network", "security-group", "enable-egress-filtering", "sg-old")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "completed") || !strings.Contains(out, "sg-old") {
		t.Errorf("output:\n%s", out)
	}
	if body := bodies["POST /api/v1/network/security-groups/sg-old/enable-egress-filtering"]; body != "" {
		t.Errorf("the conversion takes no body, sent %q", body)
	}
}
