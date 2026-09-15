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
	"io"
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
	svc := NewService(repo, store, 1024, unlimitedQuota)

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

// newS3TestBucket creates an empty bucket for the calling test and returns
// everything needed to point one or more S3BlobStores at it, with whatever
// CallTimeout each needs — the two CallTimeout tests below need two stores
// sharing one bucket, which newS3TestStore's all-in-one shape cannot give
// them.
func newS3TestBucket(t *testing.T) (endpoint, accessKey, secretKey, bucket string) {
	t.Helper()

	endpoint = os.Getenv(testS3EndpointEnv)
	if endpoint == "" {
		t.Skipf("%s not set; skipping S3 integration test (see docker-compose.yml's minio service)", testS3EndpointEnv)
	}
	accessKey = os.Getenv(testS3AccessKeyEnv)
	secretKey = os.Getenv(testS3SecretKeyEnv)
	if accessKey == "" || secretKey == "" {
		t.Fatalf("%s is set but %s/%s are not — credentials are required", testS3EndpointEnv, testS3AccessKeyEnv, testS3SecretKeyEnv)
	}

	ctx := context.Background()

	admin, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("build admin client: %v", err)
	}

	bucket = fmt.Sprintf("test-%d", time.Now().UnixNano())
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

	return endpoint, accessKey, secretKey, bucket
}

// newS3StoreForBucket builds a store for an already-created bucket, with
// callTimeout set explicitly — used to have two stores, with different
// CallTimeouts, share one bucket.
func newS3StoreForBucket(t *testing.T, endpoint, accessKey, secretKey, bucket string, callTimeout time.Duration) BlobStore {
	t.Helper()

	store, closeStore, err := NewS3BlobStore(context.Background(), S3Config{
		Endpoint:    endpoint,
		Bucket:      bucket,
		AccessKey:   accessKey,
		SecretKey:   secretKey,
		UseSSL:      false,
		CallTimeout: callTimeout,
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

// The round trip Open makes before handing back a reader (StatObject) must
// be bound by CallTimeout, the same as every other call this store makes —
// it replaced an obj.Stat() call that took no context at all and so could
// never be cancelled (issue #279).
//
// Negative control: verified failing (error nil) against a version of Open
// that called the original context-less obj.Stat() instead of
// StatObject(statCtx, ...).
func TestS3BlobStore_Open_StatIsBoundByCallTimeout(t *testing.T) {
	endpoint, accessKey, secretKey, bucket := newS3TestBucket(t)

	// A generous-timeout store just to create the object with.
	setup := newS3StoreForBucket(t, endpoint, accessKey, secretKey, bucket, 24*time.Hour)
	if _, err := setup.Put(context.Background(), "bound-by-timeout", strings.NewReader("content"), 1024); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	// A second store, sharing the bucket but pointed at an exhausted
	// timeout — the Stat this makes must fail before any data is read.
	tiny := newS3StoreForBucket(t, endpoint, accessKey, secretKey, bucket, time.Nanosecond)
	if _, err := tiny.Open(context.Background(), "bound-by-timeout"); err == nil {
		t.Fatal("Open() with an exhausted CallTimeout: error = nil, want a timeout error")
	}
}

// The point of splitting Open's Stat from its GetObject (see Open's own
// doc comment): CallTimeout must bound the round trip that proves the
// object exists, and must NOT bound reading the object's bytes afterward.
// A real download can legitimately outlast a short per-call deadline meant
// to catch a stuck round trip, not a slow-but-healthy transfer.
//
// Verified by opening successfully with a short CallTimeout, then sleeping
// past it before reading — proving the reader is not tied to a context
// that already expired.
func TestS3BlobStore_Open_StreamingOutlivesCallTimeout(t *testing.T) {
	const content = "the full content of a legitimately slow download"
	endpoint, accessKey, secretKey, bucket := newS3TestBucket(t)

	setup := newS3StoreForBucket(t, endpoint, accessKey, secretKey, bucket, 24*time.Hour)
	if _, err := setup.Put(context.Background(), "outlives-timeout", strings.NewReader(content), 1024); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	short := newS3StoreForBucket(t, endpoint, accessKey, secretKey, bucket, 50*time.Millisecond)
	rc, err := short.Open(context.Background(), "outlives-timeout")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer rc.Close()

	// Outlast the 50ms CallTimeout before reading a single byte. If Read
	// were bound by the same deadline as Stat, this would now fail.
	time.Sleep(200 * time.Millisecond)

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading after CallTimeout elapsed: error = %v, want nil (the deadline must not reach the stream)", err)
	}
	if string(got) != content {
		t.Fatalf("read %q, want %q", got, content)
	}
}
