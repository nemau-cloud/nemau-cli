package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// zoneUUID is a zone's id, a UUID, as opposed to its name.
const zoneUUID = "3f1c9a2e-7b4d-4e8a-9c61-2d5f0b8e4a17"

const pendingZones = `{"zones":[{"id":"` + zoneUUID + `","name":"example.com.","email":"dns@example.com","ttl":300,
	"status":"PENDING_VERIFICATION",
	"verification":{"name":"_nemau-challenge.example.com.","type":"TXT","value":"nemau-verification=abc123"}}],"count":1}`

func TestZoneCreateShowsTheRecordToPublish(t *testing.T) {
	srv := stub(t, map[string]string{
		"POST /api/v1/dns/zones":   `{"task_id":"task-3","status":"pending"}`,
		"GET /api/v1/tasks/task-3": `{"id":"task-3","taskType":"dns:create_zone","status":"completed","progress":100,"result":{"zone_id":"` + zoneUUID + `","status":"PENDING_VERIFICATION"}}`,
		"GET /api/v1/dns/zones":    pendingZones,
	})

	out, errOut, err := run(t, srv, "dns", "zone", "create", "example.com", "--email", "dns@example.com")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, zoneUUID) || !strings.Contains(out, "completed") {
		t.Errorf("stdout lost the task report:\n%s", out)
	}
	for _, want := range []string{
		"pending verification",
		"_nemau-challenge.example.com.  TXT  \"nemau-verification=abc123\"",
		"nemau dns zone verify " + zoneUUID,
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr is missing %q:\n%s", want, errOut)
		}
	}
}

func TestZoneCreateWithoutWaitPointsToGet(t *testing.T) {
	srv := stub(t, map[string]string{
		"POST /api/v1/dns/zones": `{"task_id":"task-3","status":"pending"}`,
	})

	_, errOut, err := run(t, srv, "--wait=false", "dns", "zone", "create", "example.com", "--email", "dns@example.com")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(errOut, `nemau dns zone get example.com`) {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestZoneGetShowsStatusAndVerification(t *testing.T) {
	srv := stub(t, map[string]string{"GET /api/v1/dns/zones": pendingZones})

	out, _, err := run(t, srv, "dns", "zone", "get", zoneUUID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{zoneUUID, "PENDING_VERIFICATION", "_nemau-challenge.example.com.", "nemau-verification=abc123"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestZoneGetSaysNothingMoreForAnActiveZone(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/dns/zones": `{"zones":[{"id":"` + zoneUUID + `","name":"example.com.","status":"ACTIVE"}],"count":1}`,
	})

	out, errOut, err := run(t, srv, "dns", "zone", "get", zoneUUID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out, "verification") || errOut != "" {
		t.Errorf("an active zone should show no verification:\n%s\nstderr: %s", out, errOut)
	}
}

// A UUID is used as is: the stub fails the test on the zone listing a
// resolution would make.
func TestZoneVerifyWaitsForTheCheck(t *testing.T) {
	srv := stub(t, map[string]string{
		"POST /api/v1/dns/zones/" + zoneUUID + "/verify": `{"task_id":"task-4","status":"pending"}`,
		"GET /api/v1/tasks/task-4":                       `{"id":"task-4","taskType":"dns:verify_zone","status":"completed","progress":100,"result":{"zone_id":"` + zoneUUID + `","status":"ACTIVE"}}`,
	})

	out, errOut, err := run(t, srv, "dns", "zone", "verify", zoneUUID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "completed") {
		t.Errorf("stdout = %s", out)
	}
	if !strings.Contains(errOut, "active") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestZoneVerifyFailureExplainsWhatToCheck(t *testing.T) {
	srv := stub(t, map[string]string{
		"POST /api/v1/dns/zones/" + zoneUUID + "/verify": `{"task_id":"task-4","status":"pending"}`,
		"GET /api/v1/tasks/task-4":                       `{"id":"task-4","taskType":"dns:verify_zone","status":"failed","progress":100,"error":"TXT record not found"}`,
	})

	_, _, err := run(t, srv, "dns", "zone", "verify", zoneUUID)
	if err == nil {
		t.Fatal("a failed verification must be an error")
	}
	for _, want := range []string{"TXT record not found", "stays pending", "nemau dns zone get " + zoneUUID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q: %v", want, err)
		}
	}
}

func TestRecordWriteOnPendingZoneSaysToVerify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"zone example.com. is pending verification","reason":"dns_zone_pending_verification"}`))
	}))
	t.Cleanup(srv.Close)

	_, _, err := run(t, srv, "dns", "record", "create", zoneUUID, "www", "--type", "A", "--record", "203.0.113.7")
	if err == nil {
		t.Fatal("expected the refusal")
	}
	if !strings.Contains(err.Error(), "nemau dns zone verify "+zoneUUID) {
		t.Errorf("error does not say how to proceed: %v", err)
	}
}

// A name is resolved through the list, with or without its trailing dot, and
// the request goes to the zone's UUID.
func TestZoneVerifyAcceptsAName(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/dns/zones":                          pendingZones,
		"POST /api/v1/dns/zones/" + zoneUUID + "/verify": `{"task_id":"task-4","status":"pending"}`,
	})

	out, _, err := run(t, srv, "--wait=false", "dns", "zone", "verify", "Example.com")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "task-4") {
		t.Errorf("stdout = %s", out)
	}
}

func TestZoneGetByNameShowsTheZone(t *testing.T) {
	srv := stub(t, map[string]string{"GET /api/v1/dns/zones": pendingZones})

	out, _, err := run(t, srv, "dns", "zone", "get", "example.com.")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, zoneUUID) {
		t.Errorf("output does not name the zone's id:\n%s", out)
	}
}

// Two zones with the same name: picking one could verify or delete the wrong
// zone.
func TestZoneNameMatchingSeveralZonesIsRefused(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/dns/zones": `{"zones":[
			{"id":"` + zoneUUID + `","name":"example.com.","status":"PENDING_VERIFICATION"},
			{"id":"8a2b6c4d-1e3f-4a5b-8c7d-9e0f1a2b3c4d","name":"example.com.","status":"ACTIVE"}],"count":2}`,
	})

	_, _, err := run(t, srv, "dns", "zone", "delete", "example.com")
	if err == nil {
		t.Fatal("an ambiguous name must be refused")
	}
	for _, want := range []string{"2 DNS zones", zoneUUID, "8a2b6c4d-1e3f-4a5b-8c7d-9e0f1a2b3c4d"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q: %v", want, err)
		}
	}
}

func TestUnknownZoneNameIsAnError(t *testing.T) {
	srv := stub(t, map[string]string{"GET /api/v1/dns/zones": pendingZones})

	_, _, err := run(t, srv, "dns", "zone", "verify", "example.org")
	if err == nil || !strings.Contains(err.Error(), `"example.org"`) {
		t.Fatalf("err = %v", err)
	}
}
