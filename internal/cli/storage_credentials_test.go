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

const fakeSecret = "EXAMPLE-SECRET-NOT-REAL-0123456789"

// fakeBucket is the store's name for "photos": what S3 tools must address.
const fakeBucket = "nemau-dev-00000000000000000000000000000000"

const issuedCredential = `{
	"access_key_id":"AKEXAMPLE0000000001",
	"secret_access_key":"` + fakeSecret + `",
	"access":"read_only",
	"container":"photos",
	"s3_bucket":"` + fakeBucket + `",
	"endpoint":"https://s3.example.test",
	"region":"fr-occitanie-1",
	"created_at":"2026-10-04T16:06:25Z",
	"expires_at":null
}`

type credentialsRequest struct{ method, path, body string }

func credentialsStub(t *testing.T, status int, body string) (*httptest.Server, *[]credentialsRequest) {
	t.Helper()
	var seen []credentialsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seen = append(seen, credentialsRequest{r.Method, r.URL.Path, string(raw)})
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/storage/containers/photos/credentials":
			if status != 0 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(issuedCredential))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/storage/containers/photos/credentials":
			_, _ = w.Write([]byte(`{"credentials":[
				{"access_key_id":"AKEXAMPLE0000000001","access":"read_only","container":"photos","created_at":"2026-10-04T16:06:25Z","expires_at":null},
				{"access_key_id":"AKEXAMPLE0000000002","access":"read_write","container":"photos","created_at":"2026-10-04T16:07:25Z","expires_at":"2026-10-05T12:00:00Z"}]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/storage/containers/photos/credentials/AKEXAMPLE0000000001":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no stub"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestCreateShowsTheSecretOnStdoutAndWarnsOnStderr(t *testing.T) {
	isolateConfig(t)
	srv, seen := credentialsStub(t, 0, "")

	stdout, stderr, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-only")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(stdout, fakeSecret) {
		t.Errorf("the secret must be on standard output:\n%s", stdout)
	}
	// The warning must not pollute stdout: a redirection has to capture the value alone.
	if strings.Contains(stdout, "does not keep it") || !strings.Contains(stderr, "does not keep it") {
		t.Errorf("warning placement: stdout=%q stderr=%q", stdout, stderr)
	}
	req := (*seen)[0]
	if req.method != "POST" || strings.Contains(req.body, "expires_at") || !strings.Contains(req.body, `"access":"read_only"`) {
		t.Errorf("request = %+v", req)
	}
}

func TestCreateRequiresAnAccessLevelAndRejectsUnknownOnes(t *testing.T) {
	isolateConfig(t)
	srv, seen := credentialsStub(t, 0, "")
	if _, _, err := run(t, srv, "storage", "credentials", "create", "photos"); err == nil {
		t.Error("create without --access succeeded")
	}
	if _, _, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "admin"); err == nil ||
		!strings.Contains(err.Error(), "read-only or read-write") {
		t.Errorf("err = %v", err)
	}
	if len(*seen) != 0 {
		t.Errorf("%d request(s) sent for invalid input", len(*seen))
	}
}

func TestCreateExpiryFlagsAreMutuallyExclusiveAndValidated(t *testing.T) {
	isolateConfig(t)
	srv, seen := credentialsStub(t, 0, "")
	if _, _, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-write",
		"--expires-in", "24h", "--expires-at", "2030-01-01T00:00:00Z"); err == nil {
		t.Error("--expires-in and --expires-at together were accepted")
	}
	if _, _, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-write",
		"--expires-at", "tomorrow"); err == nil || !strings.Contains(err.Error(), "RFC 3339") {
		t.Errorf("err = %v", err)
	}
	if len(*seen) != 0 {
		t.Errorf("%d request(s) sent for invalid input", len(*seen))
	}

	if _, _, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-write",
		"--expires-at", "2030-01-01T00:00:00Z"); err != nil {
		t.Fatalf("--expires-at: %v", err)
	}
	if !strings.Contains((*seen)[0].body, `"expires_at":"2030-01-01T00:00:00Z"`) {
		t.Errorf("body = %s", (*seen)[0].body)
	}
	if _, _, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-write",
		"--expires-in", "24h"); err != nil {
		t.Fatalf("--expires-in: %v", err)
	}
	var sent struct {
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte((*seen)[1].body), &sent); err != nil || !strings.HasSuffix(sent.ExpiresAt, "Z") {
		t.Errorf("body = %s (%v)", (*seen)[1].body, err)
	}
}

func TestCreateEnvFormatIsDirectlyUsable(t *testing.T) {
	isolateConfig(t)
	srv, _ := credentialsStub(t, 0, "")

	stdout, _, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-only", "--format", "env")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The exact variable names aws-cli and s3cmd read; a typo makes the output look right and do nothing.
	for _, want := range []string{
		"export AWS_ACCESS_KEY_ID=AKEXAMPLE0000000001",
		"export AWS_SECRET_ACCESS_KEY=" + fakeSecret,
		"export AWS_DEFAULT_REGION=fr-occitanie-1",
		"export AWS_ENDPOINT_URL=https://s3.example.test",
		"export NEMAU_S3_BUCKET=" + fakeBucket,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("env output without %q:\n%s", want, stdout)
		}
	}
	// Only exports on stdout, so `eval "$(nemau ... --format env)"` is safe.
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line != "" && !strings.HasPrefix(line, "export ") {
			t.Errorf("non-exportable line on stdout: %q", line)
		}
	}
}

func TestCreateRcloneFormatPinsPathStyle(t *testing.T) {
	isolateConfig(t)
	srv, _ := credentialsStub(t, 0, "")

	stdout, _, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-only", "--format", "rclone")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// rclone's S3 backend defaults to AWS conventions, including virtual-host
	// addressing, which this store does not use. Both lines are required.
	for _, want := range []string{"[nemau]", "type = s3", "provider = Other", "force_path_style = true", "region = fr-occitanie-1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("rclone config without %q:\n%s", want, stdout)
		}
	}
}

func TestCreateWithoutAReachableEndpointSaysSo(t *testing.T) {
	isolateConfig(t)
	srv, _ := credentialsStub(t, http.StatusCreated, strings.Replace(issuedCredential, `"https://s3.example.test"`, `""`, 1))

	_, stderr, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-only")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(stderr, "no reachable S3 endpoint") {
		t.Errorf("stderr = %q", stderr)
	}
	stdout, stderr, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-only", "--format", "env")
	if err != nil || strings.Contains(stdout, "AWS_ENDPOINT_URL") || !strings.Contains(stderr, "AWS_ENDPOINT_URL is not set") {
		t.Errorf("env without an endpoint: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
}

func TestUnknownFormatIsRejectedBeforeAnyCall(t *testing.T) {
	isolateConfig(t)
	srv, seen := credentialsStub(t, 0, "")

	_, _, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-only", "--format", "yaml")
	if err == nil || !strings.Contains(err.Error(), "yaml") || len(*seen) != 0 {
		t.Fatalf("err = %v, requests = %d", err, len(*seen))
	}
}

func TestCreateJSONCarriesEverythingNeededToConnect(t *testing.T) {
	isolateConfig(t)
	srv, _ := credentialsStub(t, 0, "")

	stdout, _, err := run(t, srv, "-o", "json", "storage", "credentials", "create", "photos", "--access", "read-only")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// A script consuming JSON needs the endpoint and region too.
	for _, want := range []string{`"access_key_id"`, `"secret_access_key"`, `"access"`, `"container"`, `"s3_bucket"`, `"endpoint"`, `"region"`, `"expires_at"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("JSON without %s:\n%s", want, stdout)
		}
	}
}

func TestCreateRefusalsCarryAHintAndKeepTheOriginalError(t *testing.T) {
	isolateConfig(t)
	srv, _ := credentialsStub(t, http.StatusTooManyRequests,
		`{"error":"this container already has the maximum number of access keys","code":"credential_limit"}`)
	_, _, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-write")
	if err == nil || !nemau.IsCredentialLimit(err) {
		t.Fatalf("the hint hid the original error: %v", err)
	}
	for _, want := range []string{
		"maximum number of access keys", // the platform's wording, untouched
		"hint: key limit reached: revoke an unused key: nemau storage credentials revoke photos <access-key-id>",
		"nemau storage credentials list photos",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}

	srv, _ = credentialsStub(t, http.StatusForbidden, `{"error":"read-only organization","code":"organization_read_only"}`)
	_, _, err = run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-write")
	if err == nil || !nemau.IsOrganizationReadOnly(err) || !strings.Contains(err.Error(), "--access read-only") {
		t.Errorf("read-only organization: %v", err)
	}

	// Any other failure is returned unchanged, without a hint.
	srv, _ = credentialsStub(t, http.StatusTooManyRequests, `{"error":"rate limit exceeded"}`)
	_, _, err = run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-write")
	if err == nil || strings.Contains(err.Error(), "hint:") {
		t.Errorf("an unclassified refusal was given a hint: %v", err)
	}
}

func TestListShowsAccessAndExpiryAndNeverASecret(t *testing.T) {
	isolateConfig(t)
	srv, _ := credentialsStub(t, 0, "")

	stdout, _, err := run(t, srv, "storage", "credentials", "list", "photos")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"AKEXAMPLE0000000001", "read_only", "read_write", "never", "2026-10-05T12:00:00Z"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("listing without %q:\n%s", want, stdout)
		}
	}
	// Secrets are not recoverable; a listing that showed one would mean the platform had stored it.
	if strings.Contains(strings.ToLower(stdout), "secret") {
		t.Errorf("the listing must expose no secret:\n%s", stdout)
	}
}

func TestRevokeNamesTheContainerAndTheKeyInThePath(t *testing.T) {
	isolateConfig(t)
	srv, seen := credentialsStub(t, 0, "")

	if _, _, err := run(t, srv, "storage", "credentials", "revoke", "photos", "AKEXAMPLE0000000001"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	last := (*seen)[len(*seen)-1]
	if last.method != "DELETE" || last.path != "/api/v1/storage/containers/photos/credentials/AKEXAMPLE0000000001" {
		t.Errorf("request = %+v", last)
	}
	// Both arguments are required; a key id alone is refused.
	if _, _, err := run(t, srv, "storage", "credentials", "revoke", "AKEXAMPLE0000000001"); err == nil {
		t.Error("revoke accepted a key id without its container")
	}
}

func TestCredentialLimitHintStatesTheCapWhenThePlatformReportsIt(t *testing.T) {
	isolateConfig(t)
	for _, tc := range []struct{ body, want string }{
		{`{"error":"limit","code":"credential_limit","limit":10,"scope":"bucket"}`, "hint: key limit reached (10 per bucket): revoke an unused key:"},
		{`{"error":"limit","code":"credential_limit","limit":50,"scope":"project"}`, "hint: key limit reached (50 per project): revoke an unused key:"},
		{`{"error":"limit","code":"credential_limit","limit":7}`, "hint: key limit reached (7): revoke an unused key:"},
		{`{"error":"limit","code":"credential_limit"}`, "hint: key limit reached: revoke an unused key:"},
	} {
		srv, _ := credentialsStub(t, http.StatusTooManyRequests, tc.body)
		_, _, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-write")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %q", tc.body, err, tc.want)
		}
	}
}

// The hints must name the bucket the key's policy grants. With the container
// name, `aws s3 sync` and `rclone sync` are refused by the store.
func TestHintsAddressTheS3BucketNotTheContainerName(t *testing.T) {
	isolateConfig(t)
	srv, _ := credentialsStub(t, 0, "")

	stdout, stderr, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-only")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(stdout, "s3_bucket") || !strings.Contains(stdout, fakeBucket) || !strings.Contains(stderr, fakeBucket) {
		t.Errorf("the bucket is not shown: stdout=%q stderr=%q", stdout, stderr)
	}

	_, stderr, err = run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-only", "--format", "env")
	if err != nil {
		t.Fatalf("env: %v", err)
	}
	if !strings.Contains(stderr, "s3 sync s3://"+fakeBucket+" ") || strings.Contains(stderr, "s3://photos") {
		t.Errorf("aws hint = %q", stderr)
	}

	_, stderr, err = run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-only", "--format", "rclone")
	if err != nil {
		t.Fatalf("rclone: %v", err)
	}
	if !strings.Contains(stderr, "rclone sync nemau:"+fakeBucket+" ") || strings.Contains(stderr, "nemau:photos") {
		t.Errorf("rclone hint = %q", stderr)
	}
}

// When the platform does not report the bucket, the hints use a placeholder
// rather than the container name, which would not work.
func TestHintsWithoutAReportedBucketUseAPlaceholder(t *testing.T) {
	isolateConfig(t)
	srv, _ := credentialsStub(t, http.StatusCreated, strings.Replace(issuedCredential, `"s3_bucket":"`+fakeBucket+`",`, "", 1))

	stdout, stderr, err := run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-only", "--format", "env")
	if err != nil {
		t.Fatalf("env: %v", err)
	}
	if strings.Contains(stdout, "NEMAU_S3_BUCKET") || strings.Contains(stderr, "s3://photos") || !strings.Contains(stderr, "s3://<s3-bucket>") {
		t.Errorf("stdout=%q stderr=%q", stdout, stderr)
	}
	_, stderr, err = run(t, srv, "storage", "credentials", "create", "photos", "--access", "read-only", "--format", "rclone")
	if err != nil || strings.Contains(stderr, "nemau:photos") || !strings.Contains(stderr, "nemau:<s3-bucket>") {
		t.Errorf("rclone: err=%v stderr=%q", err, stderr)
	}
}
