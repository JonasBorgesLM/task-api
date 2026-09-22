package attachment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// newTestMinioClient points a minio.Client at srv. A plain
// httptest.Server rather than a real MinIO: probeWritePermission's own
// contract is entirely about which two HTTP calls it makes and in what
// order, not about S3 semantics real MinIO would add nothing to
// verifying — and staying off a real network dependency here is what
// lets these run in the plain (non-integration) suite, unlike the
// AccessDenied case a real credential would need real IAM restrictions
// to produce.
func newTestMinioClient(t *testing.T, srv *httptest.Server) *minio.Client {
	t.Helper()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds: credentials.NewStaticV4("test", "test", ""),
		// A fixed region skips minio-go's own GetBucketLocation lookup
		// before the first request — this stub server only implements
		// the PUT/DELETE probeWritePermission itself makes.
		Region: "us-east-1",
		Secure: false,
	})
	if err != nil {
		t.Fatalf("build test minio client: %v", err)
	}
	return client
}

// TestProbeWritePermission_DeniedWrite_ReturnsErrorNamingIt is 16.B4's
// central case: a credential that can reach the bucket (BucketExists
// already passed, by construction — this test calls probeWritePermission
// directly) but cannot write to it must fail with a message pointing at
// write access, not a generic failure.
func TestProbeWritePermission_DeniedWrite_ReturnsErrorNamingIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>AccessDenied</Code><Message>Access Denied.</Message></Error>`))
	}))
	defer srv.Close()

	err := probeWritePermission(context.Background(), newTestMinioClient(t, srv), "test-bucket")
	if err == nil {
		t.Fatal("probeWritePermission() = nil error, want one for a denied write")
	}
	if !strings.Contains(err.Error(), "not writable") {
		t.Errorf("error = %q, want it to name the missing write permission", err)
	}
}

// TestProbeWritePermission_DeniedWrite_NeverAttemptsRemove is 16.B4's
// "no litter" acceptance criterion, from the side that matters most: a
// Put that never landed has nothing to clean up, and probeWritePermission
// must not even try — a negative assertion ("no error mentioning
// removal") would be satisfied by a call that never happened, so this
// counts the DELETE requests the test server actually received.
func TestProbeWritePermission_DeniedWrite_NeverAttemptsRemove(t *testing.T) {
	var deleteCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>Access Denied.</Message></Error>`))
		case http.MethodDelete:
			atomic.AddInt32(&deleteCalls, 1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	_ = probeWritePermission(context.Background(), newTestMinioClient(t, srv), "test-bucket")

	if got := atomic.LoadInt32(&deleteCalls); got != 0 {
		t.Errorf("RemoveObject was called %d times after a failed Put — there was nothing to remove", got)
	}
}

// TestProbeWritePermission_WritableCredential_LeavesNoObjectBehind is
// 16.B4's success path: a working credential probes writeProbeKey and
// removes exactly what it wrote.
func TestProbeWritePermission_WritableCredential_LeavesNoObjectBehind(t *testing.T) {
	var putPath, deletePath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			putPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			deletePath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	if err := probeWritePermission(context.Background(), newTestMinioClient(t, srv), "test-bucket"); err != nil {
		t.Fatalf("probeWritePermission() unexpected error: %v", err)
	}

	wantSuffix := "/test-bucket/" + writeProbeKey
	if putPath != wantSuffix {
		t.Errorf("Put path = %q, want %q", putPath, wantSuffix)
	}
	if deletePath != wantSuffix {
		t.Errorf("Delete path = %q, want %q — the object it just wrote, nothing else", deletePath, wantSuffix)
	}
}

// TestProbeWritePermission_RemoveFails_ReportsLeftoverDistinctly covers
// the branch where the object was written but could not be cleaned up —
// the error must say so distinctly, per probeWritePermission's own doc
// comment, so the startup log tells the difference between "nothing was
// written" and "something may need cleaning up by hand".
func TestProbeWritePermission_RemoveFails_ReportsLeftoverDistinctly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<Error><Code>InternalError</Code><Message>boom</Message></Error>`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	err := probeWritePermission(context.Background(), newTestMinioClient(t, srv), "test-bucket")
	if err == nil {
		t.Fatal("probeWritePermission() = nil error, want one when RemoveObject fails")
	}
	if !strings.Contains(err.Error(), "left behind") {
		t.Errorf("error = %q, want it to say the probe object was left behind", err)
	}
}
