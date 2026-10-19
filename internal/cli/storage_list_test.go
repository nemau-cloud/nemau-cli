package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// listServer serves n objects obj-000..obj-(n-1) in pages, with an opaque
// cursor ("c<offset>") and the platform's limit rules (1-1000).
func listServer(t *testing.T, n int) (*httptest.Server, *[]string) {
	t.Helper()
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		q := r.URL.Query()
		limit := 100
		if v := q.Get("limit"); v != "" {
			if _, err := fmt.Sscanf(v, "%d", &limit); err != nil || limit < 1 || limit > 1000 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"limit must be an integer between 1 and 1000"}`))
				return
			}
		}
		offset := 0
		if m := q.Get("marker"); m != "" {
			if _, err := fmt.Sscanf(m, "c%d", &offset); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid marker"}`))
				return
			}
		}
		end := offset + limit
		next := fmt.Sprintf("c%d", end)
		if end >= n {
			end, next = n, ""
		}
		objects := []map[string]any{}
		for i := offset; i < end; i++ {
			objects = append(objects, map[string]any{"name": fmt.Sprintf("obj-%04d", i), "size": 10})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"objects": objects, "next_marker": next})
	}))
	t.Cleanup(srv.Close)
	return srv, &queries
}

func TestObjectListFollowsTheCursorToTheEnd(t *testing.T) {
	srv, queries := listServer(t, 2500)
	out, errOut, err := run(t, srv, "-q", "storage", "object", "list", "photos")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	lines := strings.Fields(out)
	if len(lines) != 2500 || lines[0] != "obj-0000" || lines[2499] != "obj-2499" {
		t.Errorf("listed %d objects (%v ... %v)", len(lines), lines[:1], lines[len(lines)-1:])
	}
	if len(*queries) != 3 {
		t.Errorf("queries = %v, want 3 pages of up to 1000", *queries)
	}
	if strings.Contains(errOut, "stopped") {
		t.Errorf("a complete listing was reported as stopped: %q", errOut)
	}
}

func TestObjectListLimitStopsOnAPageBoundaryAndGivesTheCursor(t *testing.T) {
	srv, queries := listServer(t, 2500)
	out, errOut, err := run(t, srv, "storage", "object", "list", "photos", "--limit", "1200")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if n := strings.Count(out, "obj-"); n != 1200 {
		t.Errorf("listed %d objects, want 1200", n)
	}
	// Second page asks for exactly the 200 still wanted, never over the 1000 cap.
	if len(*queries) != 2 || !strings.Contains((*queries)[0], "limit=1000") || !strings.Contains((*queries)[1], "limit=200") {
		t.Errorf("queries = %v", *queries)
	}
	if !strings.Contains(errOut, "--marker c1200") {
		t.Errorf("stderr = %q, want the cursor to resume from", errOut)
	}

	// Resuming from the cursor continues where it stopped.
	out, _, err = run(t, srv, "-q", "storage", "object", "list", "photos", "--marker", "c1200", "--limit", "5")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if f := strings.Fields(out); len(f) != 5 || f[0] != "obj-1200" {
		t.Errorf("resumed listing = %v", f)
	}
}

func TestObjectListRejectsANegativeLimitLocally(t *testing.T) {
	srv, queries := listServer(t, 10)
	if _, _, err := run(t, srv, "storage", "object", "list", "photos", "--limit", "-3"); err == nil {
		t.Fatal("a negative limit was accepted")
	}
	if len(*queries) != 0 {
		t.Error("the request was sent")
	}
}

func TestObjectListJSONCarriesTheCursorOnlyWhenTruncated(t *testing.T) {
	srv, _ := listServer(t, 30)
	out, _, err := run(t, srv, "-o", "json", "storage", "object", "list", "photos", "--limit", "10")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var got struct {
		Objects    []map[string]any `json:"objects"`
		NextMarker string           `json:"next_marker"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(got.Objects) != 10 || got.NextMarker != "c10" {
		t.Errorf("got %d objects, next_marker %q", len(got.Objects), got.NextMarker)
	}
}

func TestInstanceMetricsRefusesAnUnknownPeriodLocally(t *testing.T) {
	srv := stub(t, map[string]string{})
	_, _, err := run(t, srv, "compute", "instance", "metrics", "i-1", "--period", "30d")
	if err == nil || !strings.Contains(err.Error(), "1h, 6h, 24h, 7d") {
		t.Fatalf("err = %v, want the allowed periods named", err)
	}
}
