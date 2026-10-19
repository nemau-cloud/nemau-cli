package cli

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

var generatedKey = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// keyServer answers every write with the given status/body and records the
// Idempotency-Key of each request.
func keyServer(t *testing.T, status int, body string, header http.Header) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		for k, vs := range header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), keys...) }
}

func TestWriteGeneratesAKeyByDefault(t *testing.T) {
	srv, keys := keyServer(t, http.StatusAccepted, `{"task_id":"task-1","status":"queued"}`, nil)
	if _, _, err := run(t, srv, "--wait=false", "network", "vpc", "create", "prod"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := keys(); len(got) != 1 || !generatedKey.MatchString(got[0]) {
		t.Errorf("keys = %v, want one UUID v4", got)
	}
}

func TestIdempotencyKeyFlagIsSent(t *testing.T) {
	srv, keys := keyServer(t, http.StatusAccepted, `{"task_id":"task-1","status":"queued"}`, nil)
	if _, _, err := run(t, srv, "--wait=false", "--idempotency-key", "nightly-2026-10-10", "network", "vpc", "create", "prod"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := keys(); len(got) != 1 || got[0] != "nightly-2026-10-10" {
		t.Errorf("keys = %v", got)
	}
}

func TestInvalidIdempotencyKeyIsRefusedBeforeSending(t *testing.T) {
	srv, keys := keyServer(t, http.StatusAccepted, `{"task_id":"task-1"}`, nil)
	_, _, err := run(t, srv, "--wait=false", "--idempotency-key", "not a key", "network", "vpc", "create", "prod")
	if err == nil {
		t.Fatal("an invalid key was accepted")
	}
	if got := keys(); len(got) != 0 {
		t.Errorf("%d request(s) sent with an invalid key", len(got))
	}
}

func TestReplayIsReportedOnStderr(t *testing.T) {
	srv, _ := keyServer(t, http.StatusAccepted, `{"task_id":"task-orig","status":"running"}`, http.Header{"Idempotent-Replayed": {"true"}})
	out, errOut, err := run(t, srv, "--wait=false", "--idempotency-key", "k1", "network", "vpc", "create", "prod")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(errOut, "replayed") || !strings.Contains(errOut, "task-orig") {
		t.Errorf("stderr = %q, want a replay notice naming the original task", errOut)
	}
	if strings.Contains(out, "replayed") {
		t.Errorf("the notice leaked into stdout: %q", out)
	}
}

func TestKeyMismatchGetsAHint(t *testing.T) {
	srv, keys := keyServer(t, http.StatusUnprocessableEntity, `{"error":"idempotency_key_mismatch"}`, nil)
	_, _, err := run(t, srv, "--wait=false", "--idempotency-key", "k1", "network", "vpc", "create", "prod")
	if err == nil {
		t.Fatal("expected an error")
	}
	err = explainError(err)
	if !strings.Contains(err.Error(), "idempotency_key_mismatch") || !strings.Contains(err.Error(), "hint:") {
		t.Errorf("error = %q", err)
	}
	if len(keys()) != 1 {
		t.Errorf("a 422 was retried: %d requests", len(keys()))
	}
}

func TestRateLimitErrorSaysWhenToRetry(t *testing.T) {
	srv, _ := keyServer(t, http.StatusTooManyRequests, `{"error":"rate limit exceeded","code":"rate_limited"}`,
		http.Header{"Retry-After": {"3600"}}) // beyond the SDK's cap: surfaced at once
	_, _, err := run(t, srv, "--wait=false", "network", "vpc", "create", "prod")
	if err == nil {
		t.Fatal("expected an error")
	}
	if msg := explainError(err).Error(); !strings.Contains(msg, "try again in 1h0m0s") {
		t.Errorf("error = %q", msg)
	}
}
