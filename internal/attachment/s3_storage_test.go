//go:build integration

package attachment

// Integration tests for s3BlobStore, run against a real S3-compatible
// server (MinIO, see docker-compose.yml). Build-tagged for the same
// reason the PostgreSQL tests are: a plain `go test ./...` never compiles
// this file, so it can neither run by accident nor be silently skipped
// and reported as passing.
//
// The substantive test here is TestS3BlobStore_Contract, which runs the
// *same* assertions as the filesystem store. That is what stops the two
// backends drifting: a difference between them shows up as a failing
// shared assertion rather than as behaviour that only differs in
// production.
//
//	docker compose up -d minio minio-bucket
//	make test-integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	testS3EndpointEnv  = "TEST_S3_ENDPOINT"
	testS3AccessKeyEnv = "TEST_S3_ACCESS_KEY"
	testS3SecretKeyEnv = "TEST_S3_SECRET_KEY"
)

// newS3TestStore returns a store backed by a bucket created just for the
// calling test, and removes it afterwards.
//
// A bucket per test rather than a shared one with key prefixes: the
// contract requires an empty store, and List reports the whole bucket.
// Sharing would make one test's leftovers another test's failure, in an
// order-dependent way that only shows up when the suite is run
// differently.
func newS3TestStore(t *testing.T) BlobStore {
	t.Helper()

	endpoint := os.Getenv(testS3EndpointEnv)
	if endpoint == "" {
		t.Skipf("%s not set; skipping S3 integration test (see docker-compose.yml's minio service)", testS3EndpointEnv)
	}

	accessKey := os.Getenv(testS3AccessKeyEnv)
	secretKey := os.Getenv(testS3SecretKeyEnv)
	if accessKey == "" || secretKey == "" {
		t.Fatalf("%s is set but %s/%s are not — credentials are required", testS3EndpointEnv, testS3AccessKeyEnv, testS3SecretKeyEnv)
	}

	ctx := context.Background()

	admin, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(accessKey, secretKey, ""),
		// Plain HTTP: this is the local MinIO from docker-compose, not a
		// deployment. Production sets ATTACHMENT_S3_USE_SSL.
		Secure: false,
	})
	if err != nil {
		t.Fatalf("build admin client: %v", err)
	}

	// Bucket names are DNS labels: lowercase, no underscores.
	bucket := fmt.Sprintf("test-%d", time.Now().UnixNano())
	if err := admin.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("create bucket %q: %v", bucket, err)
	}
	t.Cleanup(func() {
		for object := range admin.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
			if object.Err != nil {
				t.Errorf("list bucket for cleanup: %v", object.Err)
				return
			}
			if err := admin.RemoveObject(ctx, bucket, object.Key, minio.RemoveObjectOptions{}); err != nil {
				t.Errorf("remove object %q: %v", object.Key, err)
			}
		}
		if err := admin.RemoveBucket(ctx, bucket); err != nil {
			t.Errorf("remove bucket %q: %v", bucket, err)
		}
	})

	store, closeStore, err := NewS3BlobStore(ctx, S3Config{
		Endpoint:  endpoint,
		Bucket:    bucket,
		AccessKey: accessKey,
		SecretKey: secretKey,
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("NewS3BlobStore() unexpected error: %v", err)
	}
	t.Cleanup(func() {
		if err := closeStore(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return store
}

// TestS3BlobStore_Contract is the point of this file: the S3 store must
// satisfy exactly what the filesystem store satisfies, assertion for
// assertion.
func TestS3BlobStore_Contract(t *testing.T) {
	runBlobStoreContract(t, newS3TestStore)
}

// TestS3BlobStore_RejectsMissingBucket pins that a wrong bucket fails at
// construction rather than on somebody's first upload. Without it, a typo
// in configuration surfaces as a 500 on a user request, layers away from
// its cause.
func TestS3BlobStore_RejectsMissingBucket(t *testing.T) {
	endpoint := os.Getenv(testS3EndpointEnv)
	if endpoint == "" {
		t.Skipf("%s not set; skipping S3 integration test", testS3EndpointEnv)
	}

	_, _, err := NewS3BlobStore(context.Background(), S3Config{
		Endpoint:  endpoint,
		Bucket:    "definitely-not-a-bucket-that-exists",
		AccessKey: os.Getenv(testS3AccessKeyEnv),
		SecretKey: os.Getenv(testS3SecretKeyEnv),
		UseSSL:    false,
	})
	if err == nil {
		t.Fatal("NewS3BlobStore() with a missing bucket = nil error, want a refusal at startup")
	}
}

// TestS3BlobStore_OrphanCollectionWorksOverS3 walks the collector against
// the object store. The grace period is the part worth re-checking here:
// List's ModTime comes from the server rather than from a local stat, so
// a clock or format difference would show up as blobs looking older or
// younger than they are — and "younger than the grace period" is exactly
// the upload in flight the collector must never delete.
func TestS3BlobStore_OrphanCollectionWorksOverS3(t *testing.T) {
	store := newS3TestStore(t)
	repo := NewMemoryRepository(fixedOwnership)
	svc := NewService(repo, store, 1024)

	if _, err := store.Put(context.Background(), "11111111-1111-4111-8111-111111111111", strings.NewReader("orphaned"), 1024); err != nil {
		t.Fatalf("Put() unexpected error: %v", err)
	}

	// Fresh blob, real clock: must survive.
	deleted, err := svc.CollectOrphans(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("CollectOrphans() unexpected error: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("CollectOrphans() removed %d — a blob younger than the grace period was deleted", deleted)
	}

	// Same blob, seen from far enough in the future to be collectable.
	svc.nowFunc = func() time.Time { return time.Now().Add(2 * time.Hour) }

	deleted, err = svc.CollectOrphans(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("CollectOrphans() unexpected error: %v", err)
	}
	if deleted != 1 {
		t.Errorf("CollectOrphans() removed %d, want 1", deleted)
	}
}
