// Package backupstore is the small blob store Dockery writes its database
// backups to: the same S3-compatible bucket the registry uses (credentials
// taken from the REGISTRY_STORAGE_S3_* variables), or a local directory
// when the registry uses the filesystem driver.
package backupstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotFound is returned by Get for a missing object.
var ErrNotFound = errors.New("backupstore: not found")

// Store keeps named objects. Names are flat (no slashes).
type Store interface {
	Put(ctx context.Context, name string, data []byte) error
	Get(ctx context.Context, name string) ([]byte, error)
	// List returns object names in ascending order.
	List(ctx context.Context) ([]string, error)
	Delete(ctx context.Context, name string) error
	// Describe says where backups go, for logs and the API.
	Describe() string
}

type fsStore struct{ dir string }

// NewFS stores objects as files under dir.
func NewFS(dir string) Store { return &fsStore{dir: dir} }

func (s *fsStore) Describe() string { return "filesystem:" + s.dir }

func (s *fsStore) path(name string) string { return filepath.Join(s.dir, filepath.Base(name)) }

func (s *fsStore) Put(_ context.Context, name string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp := s.path(name) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(name))
}

func (s *fsStore) Get(_ context.Context, name string) ([]byte, error) {
	b, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

func (s *fsStore) List(_ context.Context) ([]string, error) {
	ents, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".tmp") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *fsStore) Delete(_ context.Context, name string) error {
	err := os.Remove(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
