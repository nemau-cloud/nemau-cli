package cli

import (
	"errors"
	"strings"
	"testing"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
)

const failedInUse = `{"id":"t1","taskType":"compute:delete","status":"failed","progress":0,` +
	`"error":"detach volume(s) data before deleting the instance","errorCode":"resource_in_use",` +
	`"createdAt":"2026-10-01T10:00:00Z"}`

// A delete refused because a dependant remains must say what to do next, and
// still be the platform's own error underneath.
func TestInstanceDeleteRefusedForAVolumeSaysHowToDetachIt(t *testing.T) {
	srv := stub(t, map[string]string{
		"DELETE /api/v1/compute/instances/i-1": `{"task_id":"t1","status":"pending"}`,
		"GET /api/v1/tasks/t1":                 failedInUse,
	})

	_, _, err := run(t, srv, "compute", "instance", "delete", "i-1")
	if err == nil {
		t.Fatal("expected the delete to fail")
	}
	if !nemau.IsResourceInUse(err) {
		t.Errorf("the hint hid the original error: %v", err)
	}
	var failed *nemau.TaskFailedError
	if !errors.As(err, &failed) {
		t.Errorf("errors.As no longer reaches the task failure: %v", err)
	}
	for _, want := range []string{
		"detach volume(s) data before deleting the instance", // the platform's wording, untouched
		"hint:",
		"nemau storage volume detach <volume-id> <instance-id>",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}
}

func TestNetworkDeleteRefusedPointsAtWhatSitsOnIt(t *testing.T) {
	srv := stub(t, map[string]string{
		"DELETE /api/v1/network/networks/net-1": `{"task_id":"t1","status":"pending"}`,
		"GET /api/v1/tasks/t1": strings.Replace(
			strings.Replace(failedInUse, "compute:delete", "network:delete", 1),
			"detach volume(s) data before deleting the instance", "delete the subnets on the network first", 1),
	})

	_, _, err := run(t, srv, "network", "vpc", "delete", "net-1")
	if err == nil || !strings.Contains(err.Error(), "nemau network subnet list") {
		t.Fatalf("expected a hint naming the subnet listing, got: %v", err)
	}
}

// Any other failure is returned unchanged, without a hint.
func TestOtherDeleteFailuresCarryNoHint(t *testing.T) {
	srv := stub(t, map[string]string{
		"DELETE /api/v1/compute/instances/i-1": `{"task_id":"t1","status":"pending"}`,
		"GET /api/v1/tasks/t1": `{"id":"t1","taskType":"compute:delete","status":"failed","progress":0,` +
			`"error":"the operation failed; contact support with the task id","createdAt":"2026-10-01T10:00:00Z"}`,
	})

	_, _, err := run(t, srv, "compute", "instance", "delete", "i-1")
	if err == nil {
		t.Fatal("expected the delete to fail")
	}
	if nemau.IsResourceInUse(err) || strings.Contains(err.Error(), "hint:") {
		t.Errorf("an unclassified failure was given a hint: %v", err)
	}
}
