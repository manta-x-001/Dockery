package backupstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config describes an S3-compatible bucket (AWS S3, MinIO, Ceph, OSS's
// S3 endpoint, …).
type S3Config struct {
	Endpoint  string // host[:port], no scheme; empty = AWS
	Secure    bool
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	// Prefix is prepended to every key ("" or "dir/").
	Prefix string
}

type s3Store struct {
	c   *minio.Client
	cfg S3Config
}

// NewS3 builds a client; it does not contact the bucket.
func NewS3(cfg S3Config) (Store, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("backupstore: s3 bucket is empty")
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
		if cfg.Region != "" && cfg.Region != "us-east-1" {
			endpoint = "s3." + cfg.Region + ".amazonaws.com"
		}
		cfg.Secure = true
	}
	creds := credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")
	if cfg.AccessKey == "" {
		creds = credentials.NewIAM("") // instance / task role
	}
	c, err := minio.New(endpoint, &minio.Options{Creds: creds, Secure: cfg.Secure, Region: cfg.Region})
	if err != nil {
		return nil, err
	}
	return &s3Store{c: c, cfg: cfg}, nil
}

// FromEnv picks the backend that matches the registry's storage: S3 when
// REGISTRY_STORAGE_S3_BUCKET is set (same credentials as the registry),
// otherwise the filesystem directory fsDir.
func FromEnv(fsDir string) (Store, error) {
	bucket := os.Getenv("REGISTRY_STORAGE_S3_BUCKET")
	if bucket == "" {
		return NewFS(fsDir), nil
	}
	cfg := S3Config{
		Bucket:    bucket,
		Region:    os.Getenv("REGISTRY_STORAGE_S3_REGION"),
		AccessKey: os.Getenv("REGISTRY_STORAGE_S3_ACCESSKEY"),
		SecretKey: os.Getenv("REGISTRY_STORAGE_S3_SECRETKEY"),
		Secure:    os.Getenv("REGISTRY_STORAGE_S3_SECURE") != "false",
	}
	if ep := os.Getenv("REGISTRY_STORAGE_S3_REGIONENDPOINT"); ep != "" {
		if !strings.Contains(ep, "://") {
			ep = "https://" + ep
		}
		u, err := url.Parse(ep)
		if err != nil {
			return nil, fmt.Errorf("backupstore: bad REGISTRY_STORAGE_S3_REGIONENDPOINT: %w", err)
		}
		cfg.Endpoint, cfg.Secure = u.Host, u.Scheme == "https"
	}
	// Sibling of the registry's own tree (<root>/docker/registry/v2/…), so
	// registry garbage collection never touches it.
	if root := strings.Trim(os.Getenv("REGISTRY_STORAGE_S3_ROOTDIRECTORY"), "/"); root != "" {
		cfg.Prefix = root + "/"
	}
	cfg.Prefix += "dockery-backup/"
	return NewS3(cfg)
}

func (s *s3Store) Describe() string {
	return fmt.Sprintf("s3://%s/%s", s.cfg.Bucket, s.cfg.Prefix)
}

func (s *s3Store) Put(ctx context.Context, name string, data []byte) error {
	_, err := s.c.PutObject(ctx, s.cfg.Bucket, s.cfg.Prefix+name, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

func (s *s3Store) Get(ctx context.Context, name string) ([]byte, error) {
	o, err := s.c.GetObject(ctx, s.cfg.Bucket, s.cfg.Prefix+name, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer o.Close()
	b, err := io.ReadAll(o)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return b, nil
}

func (s *s3Store) List(ctx context.Context) ([]string, error) {
	var out []string
	for o := range s.c.ListObjects(ctx, s.cfg.Bucket, minio.ListObjectsOptions{Prefix: s.cfg.Prefix}) {
		if o.Err != nil {
			return nil, o.Err
		}
		out = append(out, strings.TrimPrefix(o.Key, s.cfg.Prefix))
	}
	sort.Strings(out)
	return out, nil
}

func (s *s3Store) Delete(ctx context.Context, name string) error {
	return s.c.RemoveObject(ctx, s.cfg.Bucket, s.cfg.Prefix+name, minio.RemoveObjectOptions{})
}
