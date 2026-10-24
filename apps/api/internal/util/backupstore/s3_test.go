package backupstore

import (
	"context"
	"os"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Runs against a real S3-compatible server when TEST_S3_ENDPOINT
// (host:port), TEST_S3_ACCESSKEY and TEST_S3_SECRETKEY are set; the
// bucket is created on demand. Skipped otherwise.
func TestS3StoreRoundTrip(t *testing.T) {
	ep := os.Getenv("TEST_S3_ENDPOINT")
	if ep == "" {
		t.Skip("TEST_S3_ENDPOINT not set")
	}
	ak, sk := os.Getenv("TEST_S3_ACCESSKEY"), os.Getenv("TEST_S3_SECRETKEY")
	ctx := context.Background()
	mc, err := minio.New(ep, &minio.Options{Creds: credentials.NewStaticV4(ak, sk, "")})
	if err != nil {
		t.Fatal(err)
	}
	_ = mc.MakeBucket(ctx, "dockery-test", minio.MakeBucketOptions{})

	t.Setenv("REGISTRY_STORAGE_S3_BUCKET", "dockery-test")
	t.Setenv("REGISTRY_STORAGE_S3_ACCESSKEY", ak)
	t.Setenv("REGISTRY_STORAGE_S3_SECRETKEY", sk)
	t.Setenv("REGISTRY_STORAGE_S3_REGIONENDPOINT", "http://"+ep)
	t.Setenv("REGISTRY_STORAGE_S3_ROOTDIRECTORY", "/registry-root")
	s, err := FromEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Describe(); got != "s3://dockery-test/registry-root/dockery-backup/" {
		t.Fatalf("Describe = %s", got)
	}
	if err := s.Put(ctx, "b.json", []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "a.json", []byte("one")); err != nil {
		t.Fatal(err)
	}
	names, err := s.List(ctx)
	if err != nil || len(names) != 2 || names[0] != "a.json" {
		t.Fatalf("List = %v, %v", names, err)
	}
	if b, err := s.Get(ctx, "b.json"); err != nil || string(b) != "two" {
		t.Fatalf("Get = %q, %v", b, err)
	}
	if _, err := s.Get(ctx, "missing.json"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	if err := s.Delete(ctx, "a.json"); err != nil {
		t.Fatal(err)
	}
	if names, _ := s.List(ctx); len(names) != 1 {
		t.Fatalf("after delete: %v", names)
	}
	_ = s.Delete(ctx, "b.json")
}
