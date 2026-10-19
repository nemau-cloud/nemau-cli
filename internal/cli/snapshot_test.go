package cli

import (
	"strings"
	"testing"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
)

func TestSnapshotListShowsStorageSizeAndCost(t *testing.T) {
	srv := stub(t, map[string]string{
		"GET /api/v1/compute/snapshots": `{"snapshots":[
			{"id":"s-1","name":"before-upgrade","instance_id":"i-1","status":"active","storage":"object_store",
			 "size_bytes":2147483648,"monthly_cost":0.1,"currency":"eur","restorable":true},
			{"id":"s-2","name":"old","instance_id":"i-1","status":"active","storage":"hypervisor","restorable":false}
		],"count":2}`,
	})

	out, _, err := run(t, srv, "compute", "snapshot", "list")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"STORAGE", "SIZE", "MONTHLY", "object_store", "2.0 GiB", "0.10 EUR", "hypervisor"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	// The hypervisor snapshot has no size and no price: dashes, not zeros.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "s-2") && (strings.Contains(line, "0 B") || strings.Contains(line, "0.00")) {
			t.Errorf("an unknown size or price was rendered as zero: %q", line)
		}
	}
}

func TestSnapshotRestorePostsAndWaits(t *testing.T) {
	srv := stub(t, map[string]string{
		"POST /api/v1/compute/snapshots/s-1/restore": `{"task_id":"task-3","status":"pending"}`,
		"GET /api/v1/tasks/task-3": `{"id":"task-3","taskType":"compute:restore_snapshot","status":"completed","progress":100,
			"result":{"snapshot_id":"s-1","instance_id":"i-1"}}`,
	})

	out, _, err := run(t, srv, "compute", "snapshot", "restore", "s-1")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "completed") || !strings.Contains(out, "i-1") {
		t.Errorf("the restore did not report the instance:\n%s", out)
	}
}

func TestSnapshotMonthlyNeverReadsAsFree(t *testing.T) {
	cost := 0.004
	if got := snapshotMonthly(nemau.Snapshot{MonthlyCost: &cost, Currency: "eur"}); got != "<0.01 EUR" {
		t.Errorf("snapshotMonthly(0.004) = %q", got)
	}
	if got := snapshotMonthly(nemau.Snapshot{}); got != "-" {
		t.Errorf("snapshotMonthly(nil) = %q", got)
	}
}
