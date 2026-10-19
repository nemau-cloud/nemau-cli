package cli

import (
	"strings"
	"testing"
)

const quotasBody = `{"quotas":[
	{"scope":"tenant","dimensions":[
		{"name":"cores","unit":"count","used":95,"reserved":0,"limit":100},
		{"name":"ram","unit":"MB","used":0,"reserved":0,"limit":51200}
	]},
	{"scope":"project","project_id":"22222222-2222-2222-2222-222222222222","dimensions":[
		{"name":"cores","unit":"count","used":8,"reserved":4,"limit":32},
		{"name":"objectStorage","unit":"bytes","used":4096,"reserved":0,"limit":107374182400}
	]}
]}`

// One row per dimension, showing the scope that refuses first. The
// organization has the larger cores limit but less headroom (5 left, against 20
// for the project), so its row is the one shown.
func TestQuotaShowsTheBindingScope(t *testing.T) {
	isolateConfig(t)
	srv, _ := projectStub(t, map[string]string{"GET /api/v1/orchestration/quotas": quotasBody})

	out, _, err := run(t, srv, "quota")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var coresLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "cores") {
			coresLine = line
		}
	}
	if coresLine == "" {
		t.Fatalf("no cores row:\n%s", out)
	}
	if !strings.Contains(coresLine, "tenant") {
		t.Errorf("cores should bind on the organization scope: %q", coresLine)
	}
	// AVAILABLE subtracts reservations; the last column is 100-95-0.
	if !strings.Contains(coresLine, "100") || !strings.HasSuffix(strings.TrimSpace(coresLine), "5") {
		t.Errorf("expected limit 100 and 5 available: %q", coresLine)
	}
	// Only the binding scope by default: one row per dimension, not two.
	if strings.Count(out, "cores") != 1 {
		t.Errorf("expected a single cores row:\n%s", out)
	}
}

func TestQuotaAllScopesShowsBoth(t *testing.T) {
	isolateConfig(t)
	srv, _ := projectStub(t, map[string]string{"GET /api/v1/orchestration/quotas": quotasBody})

	out, _, err := run(t, srv, "quota", "--all-scopes")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Count(out, "cores") != 2 {
		t.Errorf("expected the project and the organization rows:\n%s", out)
	}
	// A dimension only one scope reports still appears exactly once.
	if strings.Count(out, "objectStorage") != 1 {
		t.Errorf("expected one objectStorage row:\n%s", out)
	}
}

// Units are printed as the API counted them: converting the number would stop
// it matching the limit the platform actually enforces.
func TestQuotaPrintsTheApiUnits(t *testing.T) {
	isolateConfig(t)
	srv, _ := projectStub(t, map[string]string{"GET /api/v1/orchestration/quotas": quotasBody})

	out, _, err := run(t, srv, "quota")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "51200 MB") {
		t.Errorf("RAM should stay in MB:\n%s", out)
	}
	if !strings.Contains(out, "GiB") {
		t.Errorf("object storage should be rendered in binary units:\n%s", out)
	}
}
