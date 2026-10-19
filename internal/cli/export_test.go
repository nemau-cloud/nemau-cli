package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exportStubRoutes is a small but complete project: two networks (one of them
// the region's external one), a subnet, a security group with a rule, a
// keypair, an instance wired to all of it, a volume attached to that instance,
// a floating IP associated with it, a container, and a DNS zone whose SOA is
// protected.
var exportStubRoutes = map[string]string{
	"GET /api/v1/network/networks": `{"networks":[
		{"id":"net-1","name":"app","description":"application network","status":"ACTIVE"},
		{"id":"net-ext","name":"external","external":true,"status":"ACTIVE"}]}`,
	"GET /api/v1/network/subnets": `{"subnets":[
		{"id":"sub-1","name":"app","network_id":"net-1","cidr":"10.42.0.0/24",
		 "gateway_ip":"10.42.0.1","dns_servers":["1.1.1.1"]},
		{"id":"sub-x","name":"external","network_id":"net-ext","cidr":"203.0.113.0/24"}]}`,
	"GET /api/v1/network/interfaces": `{"interfaces":[
		{"id":"if-1","instance_id":"inst-1","network_id":"net-1","slot":"net0"}]}`,
	"GET /api/v1/network/security-groups": `{"security_groups":[
		{"id":"sg-1","name":"web","description":"http",
		 "rules":[{"id":"rule-1","security_group_id":"sg-1","direction":"ingress",
		           "protocol":"tcp","port_range_min":443,"port_range_max":443,
		           "remote_ip_prefix":"0.0.0.0/0"}]},
		{"id":"sg-2","name":"db","egress_filtering":true,
		 "rules":[{"id":"rule-2","security_group_id":"sg-2","direction":"egress","remote_ip_prefix":"0.0.0.0/0"},
		          {"id":"rule-3","security_group_id":"sg-2","direction":"egress","remote_ip_prefix":"::/0"},
		          {"id":"rule-4","security_group_id":"sg-2","direction":"egress","protocol":"tcp",
		           "port_range_min":443,"port_range_max":443,"remote_ip_prefix":"0.0.0.0/0"}]}]}`,
	"GET /api/v1/compute/keypairs": `{"keypairs":[
		{"id":"kp-1","name":"deploy","public_key":"ssh-ed25519 AAAA...","type":"ssh-ed25519"}]}`,
	"GET /api/v1/compute/instances": `{"instances":[
		{"id":"inst-1","name":"web-1","status":"running",
		 "sizing":{"vcpus":2,"ram_mb":4096,"disk_gb":40},
		 "image":{"id":"ubuntu-24.04","name":"Ubuntu 24.04"},
		 "key_name":"deploy","security_groups":[{"id":"sg-1","name":"web"}]}]}`,
	"GET /api/v1/storage/volumes": `{"volumes":[
		{"id":"vol-1","name":"data","size":100,"status":"in-use",
		 "attachments":[{"id":"att-1","server_id":"inst-1","device":"/dev/sdb"}]}]}`,
	"GET /api/v1/network/floating-ips": `{"floatingips":[
		{"id":"fip-1","address":"203.0.113.7","status":"ACTIVE",
		 "target_type":"instance","target_id":"inst-1"}]}`,
	"GET /api/v1/storage/containers": `{"containers":[{"id":"c-1","name":"assets","count":12,"bytes":4096}]}`,
	"GET /api/v1/dns/zones":          `{"zones":[{"id":"zone-1","name":"example.com.","email":"dns@example.com","ttl":300}]}`,
	"GET /api/v1/dns/zones/zone-1/recordsets": `{"recordsets":[
		{"id":"example.com./SOA","name":"example.com.","type":"SOA","protected":true,"records":["ns1. dns. 1"]},
		{"id":"www.example.com./A","name":"www.example.com.","type":"A","ttl":60,"records":["203.0.113.7"]}]}`,
	"GET /api/v1/network/load-balancers": `{"load_balancers":[]}`,
	// The listing is the summary row; the topology comes from the detail.
	"GET /api/v1/kubernetes/clusters": `{"clusters":[{"id":"k-1","name":"prod","status":"active",
		"specs":{"phase":"Provisioned","kubernetes_version":"v1.35.0"}}],"count":1}`,
	"GET /api/v1/kubernetes/clusters/prod": `{"id":"k-1","name":"prod","namespace":"nemau-project-p",
		"spec":{"topology":{"version":"v1.35.0","class":"nemau-rke2","classNamespace":"nemau-classes",
		  "controlPlaneSizing":{"vcpus":2,"ram_mb":4096,"disk_gb":20},"controlPlaneReplicas":3,
		  "nodePools":[{"name":"workers","class":"default-worker","vcpus":4,"ram_mb":8192,"disk_gb":40,"replicas":3}]}},
		"status":{"phase":"Provisioned","infrastructureReady":true,"controlPlaneReady":true,"conditions":[]}}`,
}

func exportStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		if body, ok := exportStubRoutes[key]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"not available to this principal"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runExport(t *testing.T) (hcl string, imports string, dir string) {
	t.Helper()
	isolateConfig(t)
	srv := exportStub(t)
	dir = t.TempDir()

	if _, _, err := run(t, srv, "export", dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	rawHCL, err := os.ReadFile(filepath.Join(dir, "nemau.tf"))
	if err != nil {
		t.Fatalf("nemau.tf: %v", err)
	}
	rawImports, err := os.ReadFile(filepath.Join(dir, "imports.tf"))
	if err != nil {
		t.Fatalf("imports.tf: %v", err)
	}
	return string(rawHCL), string(rawImports), dir
}

func TestExportEmitsReferencesNotLiteralIDs(t *testing.T) {
	hcl, _, _ := runExport(t)

	// References make the export replayable in another project; a literal id
	// only works in the original one.
	//
	// Compared with the column padding collapsed: the alignment depends on the
	// longest attribute of each block, and pinning it would make this test fail
	// on any unrelated field addition.
	compact := collapseSpaces(hcl)
	for _, want := range []string{
		"network_id = nemau_network.app.id",
		"security_group_id = nemau_security_group.web.id",
		"volume_id = nemau_volume.data.id",
		"instance_id = nemau_instance.web-1.id",
		"zone_id = nemau_dns_zone.example_com.id",
		// Record sets wait for the ownership proof, not just for the zone.
		"zone_id = nemau_dns_zone_verification.example_com.zone_id",
		"security_group_ids = [nemau_security_group.web.id]",
		"floating_ip_id = nemau_floating_ip.r_203_0_113_7.id",
	} {
		if !strings.Contains(compact, want) {
			t.Errorf("HCL without %q:\n%s", want, hcl)
		}
	}
	// No dependency may be written as a raw id.
	for _, unwanted := range []string{`"net-1"`, `"sg-1"`, `"vol-1"`, `"zone-1"`} {
		if strings.Contains(hcl, unwanted) {
			t.Errorf("raw id %s present in the HCL instead of a reference", unwanted)
		}
	}
}

func TestExportSkipsWhatCannotBeRecreated(t *testing.T) {
	hcl, _, _ := runExport(t)

	// The region's external network exists for every project; declaring it
	// would fail on apply.
	if strings.Contains(hcl, `resource "nemau_network" "external"`) {
		t.Error("the region's external network must not be exported")
	}
	// A subnet whose parent was skipped is not exported either.
	if strings.Contains(hcl, "203.0.113.0/24") {
		t.Error("a subnet of the external network was exported")
	}
	// SOA/NS are owned by the zone itself.
	if strings.Contains(hcl, `"SOA"`) {
		t.Error("a protected record was exported")
	}
	if !strings.Contains(collapseSpaces(hcl), `type = "A"`) {
		t.Errorf("the A record is missing:\n%s", hcl)
	}
}

func TestExportCarriesSizingAndPowerState(t *testing.T) {
	hcl, _, _ := runExport(t)

	compact := collapseSpaces(hcl)
	for _, want := range []string{"vcpus = 2", "ram_mb = 4096", "disk_gb = 40"} {
		if !strings.Contains(compact, want) {
			t.Errorf("HCL without %q", want)
		}
	}
	// Power state is part of the desired state.
	if !strings.Contains(compact, "started = true") {
		t.Errorf("the power state is missing:\n%s", hcl)
	}
}

// The cluster listing carries no topology: the export has to read each
// cluster's detail to write its sizing and node pools.
func TestExportReadsClusterTopologyFromTheDetail(t *testing.T) {
	hcl, _, _ := runExport(t)

	compact := collapseSpaces(hcl)
	for _, want := range []string{
		`resource "nemau_kubernetes_cluster" "prod"`,
		`kubernetes_version = "v1.35.0"`,
		"control_plane_replicas = 3",
		`resource "nemau_kubernetes_node_pool"`,
		"ram_mb = 8192",
	} {
		if !strings.Contains(compact, want) {
			t.Errorf("HCL without %q:\n%s", want, hcl)
		}
	}
}

func TestExportWritesImportBlocksWithRealIDs(t *testing.T) {
	_, imports, _ := runExport(t)

	// The ids appear only in imports.tf, so that applying the configuration to
	// its own project adopts the resources instead of duplicating them.
	for _, want := range []string{
		"to = nemau_network.app",
		`id = "net-1"`,
		"to = nemau_instance.web-1",
		`id = "inst-1"`,
		`id = "vol-1:inst-1"`,
	} {
		if !strings.Contains(imports, want) {
			t.Errorf("imports.tf without %q:\n%s", want, imports)
		}
	}
	// The zone is imported; its ownership proof has nothing to adopt, and
	// sharing the zone's id must not have displaced the zone's own import.
	if !strings.Contains(imports, "to = nemau_dns_zone.example_com") {
		t.Errorf("the zone lost its import block:\n%s", imports)
	}
	if strings.Contains(imports, "nemau_dns_zone_verification") {
		t.Errorf("the ownership proof has an import block:\n%s", imports)
	}
}

func TestExportDegradesOnUnreadableServices(t *testing.T) {
	// Exports are often run with a narrowly scoped credential. The stub answers
	// 403 for everything not listed.
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/network/networks" {
			_, _ = w.Write([]byte(`{"networks":[{"id":"net-1","name":"app"}]}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"not authorized"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	_, stderr, err := run(t, srv, "export", dir)
	if err != nil {
		t.Fatalf("a partial export must succeed: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "nemau.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `resource "nemau_network" "app"`) {
		t.Error("what was readable was not exported")
	}
	// Skipped services must be reported, or the export would look complete.
	if !strings.Contains(stderr, "skipped") {
		t.Errorf("unreadable services are not reported: %q", stderr)
	}
}

func TestExportContainsNoSecret(t *testing.T) {
	hcl, _, dir := runExport(t)

	// The export is a file people commit, so it must not contain a private key,
	// a kubeconfig or an access key. The test looks for real secret markers
	// rather than words: the generated header mentions "kubeconfig" when it
	// says there is none.
	for _, forbidden := range []string{
		"PRIVATE KEY",             // PEM
		"client-certificate-data", // kubeconfig
		"client-key-data",
		"X-Amz-Signature", // presigned URL
		"X-Access-Key-Secret",
	} {
		if strings.Contains(hcl, forbidden) {
			t.Errorf("the HCL contains %q", forbidden)
		}
	}
	// Only the public half of an SSH key.
	if !strings.Contains(hcl, "ssh-ed25519 AAAA") {
		t.Error("the public key should be exported")
	}

	inventory, err := os.ReadFile(filepath.Join(dir, "inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(inventory), "PRIVATE KEY") {
		t.Error("the inventory contains a private key")
	}
}

func TestExportNamesAreValidTerraformIdentifiers(t *testing.T) {
	document := newHCLDocument()

	// A zone name ends with a dot; an instance name may contain any character.
	if got := document.name("nemau_dns_zone", "example.com."); got != "example-com" && got != "example_com" {
		if strings.HasSuffix(got, ".") || strings.Contains(got, ".") {
			t.Errorf("name %q is invalid for Terraform", got)
		}
	}
	if got := document.name("nemau_instance", "9-lives"); strings.HasPrefix(got, "9") {
		t.Errorf("name %q starts with a digit", got)
	}
	if got := document.name("nemau_instance", "!!!"); got == "" {
		t.Error("a wholly invalid name must produce a fallback")
	}

	// Two resources of one type sharing a name must not collide.
	first := document.name("nemau_network", "app")
	second := document.name("nemau_network", "app")
	if first == second {
		t.Errorf("name collision: %q and %q", first, second)
	}
}

// collapseSpaces squeezes runs of spaces so an assertion can name an attribute
// assignment without depending on the emitter's column alignment.
func collapseSpaces(text string) string {
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}
	return text
}

// A filtering group recreates its two allow-all egress rules itself: exporting
// them would fail on apply elsewhere. Any other rule is exported, and a rule's
// import id names its group.
func TestExportLeavesOutTheDefaultEgressRules(t *testing.T) {
	hcl, imports, _ := runExport(t)

	if strings.Contains(hcl, `"::/0"`) {
		t.Errorf("the ::/0 default egress rule was exported:\n%s", hcl)
	}
	compact := collapseSpaces(hcl)
	if strings.Count(compact, `remote_ip_prefix = "0.0.0.0/0"`) != 2 {
		t.Errorf("want the web ingress rule and the db 443 egress rule, and not the 0.0.0.0/0 default:\n%s", hcl)
	}
	if !strings.Contains(hcl, "not exported") || !strings.Contains(hcl, "<group id>:<rule id>") {
		t.Errorf("the db group does not say how to take its default rules over:\n%s", hcl)
	}
	for _, want := range []string{`id = "sg-1:rule-1"`, `id = "sg-2:rule-4"`} {
		if !strings.Contains(imports, want) {
			t.Errorf("imports.tf without %q:\n%s", want, imports)
		}
	}
	for _, unwanted := range []string{"rule-2", "rule-3"} {
		if strings.Contains(imports, unwanted) {
			t.Errorf("imports.tf adopts default rule %s:\n%s", unwanted, imports)
		}
	}
}

// provider_engine is read-only in the Terraform provider, so the export must
// not write it, even for an older load balancer that runs another engine.
func TestExportLeavesTheLoadBalancerEngineOut(t *testing.T) {
	key := "GET /api/v1/network/load-balancers"
	previous := exportStubRoutes[key]
	exportStubRoutes[key] = `{"load_balancers":[{"id":"lb-1","name":"edge","vip_subnet_id":"sub-1","vip_address":"10.42.0.9",
		"provider":"haproxy","flavor":"lb-ha","listeners":[],"pools":[]}]}`
	t.Cleanup(func() { exportStubRoutes[key] = previous })

	hcl, _, _ := runExport(t)
	if !strings.Contains(hcl, `resource "nemau_load_balancer" "edge"`) {
		t.Fatalf("the load balancer was not exported:\n%s", hcl)
	}
	if strings.Contains(hcl, "provider_engine") || strings.Contains(hcl, "haproxy") {
		t.Fatalf("the export writes the engine:\n%s", hcl)
	}
	if !strings.Contains(hcl, "# Current VIP: 10.42.0.9") {
		t.Errorf("the current VIP is not noted, in English, above the block:\n%s", hcl)
	}
}

// A certificate cannot be exported, since its private key is never read back;
// the listener that serves one says which resource to declare instead.
func TestExportNamesTheCertificateResourceOnAnHTTPSListener(t *testing.T) {
	key := "GET /api/v1/network/load-balancers"
	previous := exportStubRoutes[key]
	exportStubRoutes[key] = `{"load_balancers":[{"id":"lb-1","name":"edge","vip_subnet_id":"sub-1","flavor":"lb-small",
		"listeners":[{"id":"lis-1","name":"https","protocol":"TERMINATED_HTTPS","protocol_port":443,"tls_certificate_ref":"crt-1"}],
		"pools":[]}]}`
	t.Cleanup(func() { exportStubRoutes[key] = previous })

	hcl, _, _ := runExport(t)
	if !strings.Contains(hcl, `tls_certificate_ref = "crt-1"`) {
		t.Fatalf("the listener lost its certificate:\n%s", hcl)
	}
	if !strings.Contains(hcl, "nemau_load_balancer_certificate") || strings.Contains(hcl, "nemau_lb_certificate") {
		t.Fatalf("the note does not name nemau_load_balancer_certificate:\n%s", hcl)
	}
	if strings.Contains(hcl, `resource "nemau_load_balancer_certificate"`) {
		t.Fatalf("a certificate was exported without its key:\n%s", hcl)
	}
}
