package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The PEM files below are placeholders: the CLI relays their content as read,
// and the API does the validation.
const (
	fakeCertificatePEM = "-----BEGIN CERTIFICATE-----\nCERT\n-----END CERTIFICATE-----\n"
	fakeKeyPEM         = "-----BEGIN PRIVATE KEY-----\nKEY-MATERIAL\n-----END PRIVATE KEY-----\n"
)

func certificateJSON(notAfter time.Time) string {
	return `{"id":"crt-1","name":"www","fingerprint_sha256":"AB:CD","subject":"www.example.com",
		"domains":["www.example.com","example.com"],"issuer":"Example CA (R11)",
		"not_before":"2026-09-01T00:00:00Z","not_after":"` + notAfter.UTC().Format(time.RFC3339) + `",
		"key_algorithm":"ECDSA-P256","chain_length":1,
		"used_by":[{"listener_id":"lis-1","listener_name":"https","load_balancer_id":"lb-1"}],
		"created_at":"2026-09-23T10:00:00Z"}`
}

func writePEM(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCertificateImportSendsTheFilesAndNeverPrintsTheKey(t *testing.T) {
	isolateConfig(t)
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/network/load-balancer-certificates" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(certificateJSON(time.Now().Add(90 * 24 * time.Hour))))
	}))
	t.Cleanup(srv.Close)

	stdout, stderr, err := run(t, srv, "lb", "certificate", "import", "--name", "www",
		"--certificate", writePEM(t, "fullchain.pem", fakeCertificatePEM),
		"--private-key", writePEM(t, "privkey.pem", fakeKeyPEM))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if body["name"] != "www" || body["certificate"] != fakeCertificatePEM || body["private_key"] != fakeKeyPEM {
		t.Fatalf("body = %v", body)
	}
	if _, sent := body["chain"]; sent {
		t.Fatal("an absent chain was sent")
	}
	if strings.Contains(stdout+stderr, "KEY-MATERIAL") {
		t.Fatal("the private key was printed")
	}
	for _, want := range []string{"crt-1", "www.example.com,example.com", "Example CA (R11)", "https"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output is missing %q:\n%s", want, stdout)
		}
	}
}

func TestCertificateImportRefusesANonPEMFile(t *testing.T) {
	isolateConfig(t)
	srv := stub(t, map[string]string{})
	_, _, err := run(t, srv, "lb", "certificate", "import", "--name", "www",
		"--certificate", writePEM(t, "cert.der", "not pem"),
		"--private-key", writePEM(t, "privkey.pem", fakeKeyPEM))
	if err == nil || !strings.Contains(err.Error(), "--certificate") || !strings.Contains(err.Error(), "not a PEM file") {
		t.Fatalf("err = %v", err)
	}
}

func TestCertificateListFlagsASoonExpiringCertificate(t *testing.T) {
	isolateConfig(t)
	srv := stub(t, map[string]string{
		"GET /api/v1/network/load-balancer-certificates": `{"certificates":[` +
			certificateJSON(time.Now().Add(10*24*time.Hour)) + `],"count":1}`,
	})
	out, _, err := run(t, srv, "lb", "certificate", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"crt-1", "www", "Example CA (R11)", "(in ", "days)", "https"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestCertificateDeleteInUseSaysWhatToDo(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/network/load-balancer-certificates/crt-1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"certificate is used by a listener","reason":"certificate_in_use"}`))
	}))
	t.Cleanup(srv.Close)
	_, _, err := run(t, srv, "lb", "certificate", "delete", "crt-1")
	if err == nil || !strings.Contains(err.Error(), "--tls-certificate") {
		t.Fatalf("err = %v", err)
	}
}

func TestListenerUpdateRepointsTheCertificate(t *testing.T) {
	isolateConfig(t)
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/network/listeners/lis-1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"task_id":"t","status":"pending"}`))
	}))
	t.Cleanup(srv.Close)
	if _, _, err := run(t, srv, "--wait=false", "lb", "listener", "update", "lis-1", "--tls-certificate", "crt-2"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(body) != 1 || body["tls_certificate_ref"] != "crt-2" {
		t.Fatalf("body = %v", body)
	}
}
