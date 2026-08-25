package attachment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config describes the object store an s3BlobStore talks to. It is
// deliberately provider-neutral: the same fields address MinIO in
// development and S3 (or any S3-compatible service) in production, which
// is what keeps the two environments on one code path instead of leaving
// one of them exercised only in production.
type S3Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string

	// SecretKey must never be logged. Nothing in this package writes it
	// anywhere, and nothing anywhere logs a whole Config — keep both
	// true.
	SecretKey string

	Region string
	UseSSL bool
}

// s3BlobStore stores blobs as objects in a bucket.
//
// It exists because a pod's local disk is not shared with the pod that
// replaces it during a rolling update, and is gone entirely if the pod
// is rescheduled onto another node — so the filesystem store cannot back
// a deployment that must survive either. See docs/DECISIONS.md.
//
// Note what did *not* have to change for this to exist: the BlobStore
// interface, Service, Handler, Repository, or any test above this file.
// That was the bet made when metadata and bytes were split into two
// boundaries back in the attachments work, and this is it paying off.
type s3BlobStore struct {
	client *minio.Client
	bucket string
}

// NewS3BlobStore returns a BlobStore backed by the bucket in cfg, and
// verifies at construction that the bucket is actually reachable.
//
// The check is not ceremony. Without it, a wrong endpoint or a missing
// bucket surfaces on the first upload a user attempts — long after
// startup, as a 500 on somebody's request, with the cause several layers
// away. This is the same reasoning behind pinging the database in
// openDatabase rather than letting sql.Open's lazy connection fail later.
//
// The returned close function exists to satisfy the same shape as
// NewFSBlobStore; minio.Client holds no resource that must be released,
// so it is a no-op rather than a pretence that something is being closed.
func NewS3BlobStore(ctx context.Context, cfg S3Config) (BlobStore, func() error, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("attachment: build s3 client: %w", err)
	}

	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	exists, err := client.BucketExists(checkCtx, cfg.Bucket)
	if err != nil {
		// The error is wrapped without the credentials that produced it:
		// minio's error text carries the endpoint, which is fine, but a
		// caller logging this must never end up with the secret.
		return nil, nil, fmt.Errorf("attachment: reach bucket %q: %w", cfg.Bucket, err)
	}
	if !exists {
		// Not created on demand, for the same reason pathguard refuses a
		// missing root: silently creating it turns a typo in
		// configuration into an empty bucket nobody notices, and in a
		// shared account it can also mean creating a bucket in the wrong
		// place.
		return nil, nil, fmt.Errorf("attachment: bucket %q does not exist", cfg.Bucket)
	}

	return &s3BlobStore{client: client, bucket: cfg.Bucket}, func() error { return nil }, nil
}

func (s *s3BlobStore) Put(ctx context.Context, key string, r io.Reader, maxBytes int64) (int64, error) {
	// Read one byte past the limit, exactly as the filesystem store
	// does: if that byte exists the stream was over the limit, and we
	// know without buffering the whole thing to find out.
	//
	// Unlike the filesystem store, the oversize bytes have already
	// crossed the network by the time we can tell — there is no way to
	// know a stream's true length without reading it. The object is
	// removed below so nothing is left occupying the key.
	info, err := s.client.PutObject(ctx, s.bucket, key, io.LimitReader(r, maxBytes+1), -1,
		minio.PutObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("attachment: put object: %w", err)
	}

	if info.Size > maxBytes {
		if removeErr := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); removeErr != nil {
			// The upload is refused either way; a failed cleanup leaves
			// an object the orphan collector will reclaim later, and
			// reporting it instead of ErrTooLarge would tell the caller
			// the wrong thing about their request.
			_ = removeErr
		}
		return 0, ErrTooLarge
	}

	return info.Size, nil
}

func (s *s3BlobStore) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, mapS3Error(err)
	}

	// GetObject is lazy: it returns an object handle without having
	// talked to the server, so a missing key surfaces on the first read
	// rather than here. Stat forces that round trip now, which is what
	// lets this return ErrNotFound like the filesystem store does
	// instead of handing back a reader that fails later.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, mapS3Error(err)
	}

	return obj, nil
}

func (s *s3BlobStore) Delete(ctx context.Context, key string) error {
	// S3 delete is idempotent: removing an absent key succeeds, which is
	// the contract BlobStore.Delete already promises.
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("attachment: delete object: %w", err)
	}
	return nil
}

func (s *s3BlobStore) List(ctx context.Context) ([]BlobRef, error) {
	// Cancel the underlying listing when this returns, so an early exit
	// does not leave the client draining pages nobody will read.
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	refs := make([]BlobRef, 0)
	for object := range s.client.ListObjects(listCtx, s.bucket, minio.ListObjectsOptions{Recursive: true}) {
		if object.Err != nil {
			return nil, fmt.Errorf("attachment: list objects: %w", object.Err)
		}
		refs = append(refs, BlobRef{Key: object.Key, ModTime: object.LastModified})
	}

	return refs, nil
}

// mapS3Error translates a missing object into this package's ErrNotFound
// and leaves everything else wrapped.
//
// Both a missing key and a missing bucket map to ErrNotFound. That is not
// sloppiness: from a caller's position there is nothing to read either
// way, and Service.Download already reports a row whose blob is gone the
// same way it reports a row that does not exist — telling them apart
// would expose an internal fault to a client who can do nothing with it.
// A missing bucket is caught at startup by NewS3BlobStore anyway.
func mapS3Error(err error) error {
	resp := minio.ToErrorResponse(err)
	switch {
	case resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket":
		return ErrNotFound
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case errors.Is(err, io.EOF):
		return ErrNotFound
	default:
		return fmt.Errorf("attachment: get object: %w", err)
	}
}
