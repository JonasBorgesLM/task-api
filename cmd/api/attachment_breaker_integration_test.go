//go:build integration

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/config"
)

// s3TestConfig returns testConfig() with attachments backed by the real
// MinIO from docker-compose, skipping the calling test if TEST_S3_ENDPOINT
// is unset — the same convention internal/attachment/s3_storage_test.go
// uses, so `make test-integration` (which sets it) runs this and a plain
// `go test -tags=integration ./...` without MinIO up skips it instead of
// failing.
func s3TestConfig(t *testing.T) config.Config {
	t.Helper()

	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_S3_ENDPOINT not set; skipping S3 integration test (see docker-compose.yml's minio service)")
	}

	cfg := testConfig()
	cfg.AttachmentS3Endpoint = endpoint
	// The bucket docker-compose.yml's minio-bucket service creates on
	// startup (`mc mb --ignore-existing local/task-api-attachments`) —
	// NewS3BlobStore refuses to create one itself, so this must already
	// exist.
	cfg.AttachmentS3Bucket = "task-api-attachments"
	cfg.AttachmentS3AccessKey = os.Getenv("TEST_S3_ACCESS_KEY")
	cfg.AttachmentS3SecretKey = os.Getenv("TEST_S3_SECRET_KEY")
	cfg.AttachmentMaxBytes = 1 << 20
	cfg.AttachmentMaxBytesPerUser = 10 << 20
	cfg.AttachmentOrphanMinAge = time.Hour
	return cfg
}

// TestIntegration_DebugVars_AttachmentBreaker_ReportsClosedWithHealthyS3
// is 16.B5's positive case, the mirror of
// TestIntegration_DebugVars_AttachmentBreaker_ReportsDisabledWithoutS3 in
// main_integration_test.go: with a healthy S3 endpoint actually wired
// in through buildBlobStore, both breakers must report StateClosed, not
// "disabled" — proving newServer really does build and register them
// for the S3 branch, not only the fs/off branches the other test covers.
func TestIntegration_DebugVars_AttachmentBreaker_ReportsClosedWithHealthyS3(t *testing.T) {
	srv := httptest.NewServer(newTestServer(t, s3TestConfig(t), discardLogger()).Handler)
	defer srv.Close()
	token := registerAndLogin(t, srv)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/debug/vars", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /debug/vars: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /debug/vars body: %v", err)
	}

	for _, key := range []string{"attachment_s3_breaker", "attachment_s3_list_breaker"} {
		raw, ok := body[key]
		if !ok {
			t.Fatalf("/debug/vars response is missing expected key %q", key)
		}
		var v attachmentBreakerVars
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("decode %q: %v", key, err)
		}
		if v.State != "closed" {
			t.Errorf("%s.state = %q, want %q — a healthy S3 endpoint must not report disabled or open", key, v.State, "closed")
		}
		if v.FailureThreshold <= 0 {
			t.Errorf("%s.failure_threshold = %d, want a positive value (the breaker's own configured threshold)", key, v.FailureThreshold)
		}
	}
}
