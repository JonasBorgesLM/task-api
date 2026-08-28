package attachment

// Unlike s3_storage_test.go, this file carries no build tag and needs no
// real MinIO — it exercises NewS3BlobStore's failure path against an
// address nothing is listening on, so it runs as part of the ordinary
// unit suite.

import (
	"context"
	"net"
	"strings"
	"testing"
)

// TestNewS3BlobStore_SecretKeyNeverLeaksOnFailure locks in a property
// s3_storage.go's own doc comment on NewS3BlobStore already claims ("the
// error is wrapped without the credentials that produced it") but that,
// until this test, nothing actually verified. The guarantee holds today
// because minio-go's own error formatting never includes the credentials
// used to sign a request — confirmed empirically, not something task-api's
// wrapping does itself. A future minio-go version, or a future change to
// how the error here is wrapped, is exactly what this test would catch.
func TestNewS3BlobStore_SecretKeyNeverLeaksOnFailure(t *testing.T) {
	const secretKey = "s3-secret-do-not-log-me"

	// A listener that is opened and immediately closed leaves the port
	// free but nothing bound to it, so BucketExists fails fast with
	// "connection refused" instead of waiting out a network timeout.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find a free port: %v", err)
	}
	endpoint := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("failed to close probe listener: %v", err)
	}

	_, _, err = NewS3BlobStore(context.Background(), S3Config{
		Endpoint:  endpoint,
		Bucket:    "some-bucket",
		AccessKey: "s3-access-key-id",
		SecretKey: secretKey,
		UseSSL:    false,
	})
	if err == nil {
		t.Fatal("NewS3BlobStore() against an unreachable endpoint: expected an error, got nil")
	}

	if strings.Contains(err.Error(), secretKey) {
		t.Errorf("NewS3BlobStore() error contains the S3 secret key: %v", err)
	}
}
