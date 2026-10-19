package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpdateSendsOnlyTheFlagsGiven(t *testing.T) {
	isolateConfig(t)
	var body map[string]any
	srv := recordingStub(t, &body)

	if _, _, err := run(t, srv, "network", "vpc", "update", "net-1",
		"--description", "prod segment"); err != nil {
		t.Fatalf("update: %v", err)
	}
	// A flag the user did not type must not reach the request: sending it would
	// blank a name the user never mentioned.
	if _, present := body["name"]; present {
		t.Errorf("an untyped flag was sent: %v", body)
	}
	if body["description"] != "prod segment" {
		t.Errorf("description = %v", body["description"])
	}
}

func TestUpdateCanClearAField(t *testing.T) {
	isolateConfig(t)
	var body map[string]any
	srv := recordingStub(t, &body)

	// --description "" is a real change: the command asks cobra which flags
	// were set instead of reading their values.
	if _, _, err := run(t, srv, "network", "vpc", "update", "net-1", "--description", ""); err != nil {
		t.Fatalf("update: %v", err)
	}
	value, present := body["description"]
	if !present || value != "" {
		t.Errorf("body = %v, want an explicit empty description", body)
	}
}

func TestUpdateWithoutAnyFlagIsRefused(t *testing.T) {
	isolateConfig(t)
	var body map[string]any
	srv := recordingStub(t, &body)

	_, _, err := run(t, srv, "network", "vpc", "update", "net-1")
	if err == nil {
		t.Fatal("an update with no flag must fail")
	}
	// The refusal lists what the command does accept.
	if !strings.Contains(err.Error(), "--description") {
		t.Errorf("err = %v, does not name the available flags", err)
	}
	if body != nil {
		t.Error("a request was sent for an update that changes nothing")
	}
}

// recordingStub answers a PATCH with a queued task, then that task as
// completed, and captures the request body along the way.
func recordingStub(t *testing.T, body *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			_ = json.NewDecoder(r.Body).Decode(body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"task_id":"t-1","status":"pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"t-1","taskType":"network:update","status":"completed","progress":100,"createdAt":"2027-01-01T00:00:00Z"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}
