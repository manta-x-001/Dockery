package biz

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"api/internal/util/backupstore"

	"github.com/go-kratos/kratos/v2/log"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/scrypt"
)

// BackupConfig tunes the scheduled backup of SQLite state that cannot be
// rebuilt from registry storage: users (name, role, disabled), their repo
// permission patterns, and explicit namespaces. Password hashes are
// deliberately NOT part of a backup.
type BackupConfig struct {
	Disabled bool
	Interval time.Duration
	// Keep is how many backups are retained.
	Keep int
	// Passphrase, when set, encrypts backups (AES-256-GCM, scrypt-derived
	// key). Required again to restore them.
	Passphrase string
}

const backupMagic = "DKB1"

var (
	ErrBackupNotFound  = errors.New("backup: not found")
	ErrBackupNeedsPass = errors.New("backup: encrypted, set DOCKERY_BACKUP_PASSPHRASE to restore it")
	ErrBackupBadPass   = errors.New("backup: wrong passphrase or corrupt data")
)

type backupUser struct {
	Username string   `json:"username"`
	Role     string   `json:"role"`
	Disabled bool     `json:"disabled,omitempty"`
	Patterns []string `json:"patterns,omitempty"`
}

type backupNamespace struct {
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	CreatedBy   string `json:"created_by,omitempty"`
}

type backupPayload struct {
	Version    int               `json:"version"`
	Users      []backupUser      `json:"users"`
	Namespaces []backupNamespace `json:"namespaces"`
}

// BackupInfo describes one stored backup.
type BackupInfo struct {
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	Users     int    `json:"users"`
	Encrypted bool   `json:"encrypted"`
}

// BackupResult is the outcome of Run.
type BackupResult struct {
	Name    string `json:"name,omitempty"`
	Skipped bool   `json:"skipped"`
	Users   int    `json:"users"`
	Storage string `json:"storage"`
}

// RestoreResult counts what Restore changed.
type RestoreResult struct {
	UsersCreated     int `json:"users_created"`
	UsersUpdated     int `json:"users_updated"`
	PermissionsAdded int `json:"permissions_added"`
	NamespacesAdded  int `json:"namespaces_added"`
	// NeedPassword lists restored users that have no usable password yet;
	// an admin must set one before they can log in.
	NeedPassword []string `json:"need_password"`
}

// BackupUsecase snapshots users / permissions / namespaces into the same
// object storage the registry uses (or a local directory for the
// filesystem driver), so they survive loss of the SQLite file.
//
// Object names are "<UTC timestamp>_<content hash8>_<users>.json[.enc]":
// the hash lets Run skip unchanged state and the user count powers the
// fresh-database guard, both without downloading the object.
type BackupUsecase struct {
	users  UserRepo
	perms  PermissionRepo
	ns     NamespaceRepo
	store  backupstore.Store
	audit  *AuditUsecase
	cfg    BackupConfig
	logger *log.Helper

	mu     sync.Mutex
	once   sync.Once
	cancel context.CancelFunc
}

func NewBackupUsecase(users UserRepo, perms PermissionRepo, ns NamespaceRepo, store backupstore.Store, audit *AuditUsecase, cfg BackupConfig, logger log.Logger) *BackupUsecase {
	if cfg.Interval <= 0 {
		cfg.Interval = 24 * time.Hour
	}
	if cfg.Keep <= 0 {
		cfg.Keep = 14
	}
	return &BackupUsecase{
		users: users, perms: perms, ns: ns, store: store, audit: audit, cfg: cfg,
		logger: log.NewHelper(log.With(logger, "module", "biz/backup")),
	}
}

// Start runs the first backup shortly after boot, then every Interval.
func (u *BackupUsecase) Start() {
	if u.cfg.Disabled {
		return
	}
	u.once.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		u.cancel = cancel
		u.logger.Infof("scheduled backups every %s to %s", u.cfg.Interval, u.store.Describe())
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			t := time.NewTicker(u.cfg.Interval)
			defer t.Stop()
			for {
				if _, err := u.Run(ctx, "scheduler"); err != nil {
					u.logger.Warnf("scheduled backup: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	})
}

func (u *BackupUsecase) Stop() {
	if u.cancel != nil {
		u.cancel()
	}
}

func (u *BackupUsecase) snapshot(ctx context.Context) (*backupPayload, error) {
	users, err := u.users.List(ctx)
	if err != nil {
		return nil, err
	}
	p := &backupPayload{Version: 1, Users: []backupUser{}, Namespaces: []backupNamespace{}}
	for _, usr := range users {
		perms, err := u.perms.ListForUser(ctx, usr.ID)
		if err != nil {
			return nil, err
		}
		bu := backupUser{Username: usr.Username, Role: usr.Role, Disabled: usr.Disabled}
		for _, pm := range perms {
			bu.Patterns = append(bu.Patterns, pm.RepoPattern)
		}
		sort.Strings(bu.Patterns)
		p.Users = append(p.Users, bu)
	}
	sort.Slice(p.Users, func(i, j int) bool { return p.Users[i].Username < p.Users[j].Username })
	nss, err := u.ns.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nss {
		p.Namespaces = append(p.Namespaces, backupNamespace{Path: n.Path, Description: n.Description, CreatedBy: n.CreatedBy})
	}
	sort.Slice(p.Namespaces, func(i, j int) bool { return p.Namespaces[i].Path < p.Namespaces[j].Path })
	return p, nil
}

// parseBackupName splits "<ts>_<hash8>_<users>.json[.enc]".
func parseBackupName(name string) (ts time.Time, hash string, users int, enc, ok bool) {
	base, enc := strings.CutSuffix(name, ".enc")
	base, isJSON := strings.CutSuffix(base, ".json")
	parts := strings.Split(base, "_")
	if !isJSON || len(parts) != 3 {
		return time.Time{}, "", 0, false, false
	}
	t, err := time.Parse("20060102T150405.000Z", parts[0])
	n, err2 := strconv.Atoi(parts[2])
	if err != nil || err2 != nil {
		return time.Time{}, "", 0, false, false
	}
	return t, parts[1], n, enc, true
}

// names returns valid backup object names, newest first.
func (u *BackupUsecase) names(ctx context.Context) ([]string, error) {
	all, err := u.store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range all {
		if _, _, _, _, ok := parseBackupName(n); ok {
			out = append(out, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

// Run takes a backup now. It is a no-op (Skipped) when nothing changed
// since the newest stored backup.
func (u *BackupUsecase) Run(ctx context.Context, actor string) (*BackupResult, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	p, err := u.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	plain, _ := json.Marshal(p) // struct order + sorted slices → deterministic
	sum := sha256.Sum256(plain)
	hash := hex.EncodeToString(sum[:])[:8]
	res := &BackupResult{Users: len(p.Users), Storage: u.store.Describe()}

	names, err := u.names(ctx)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	if len(names) > 0 {
		_, lastHash, lastUsers, _, _ := parseBackupName(names[0])
		if lastHash == hash {
			res.Skipped = true
			return res, nil
		}
		// A freshly bootstrapped database (just the first admin) next to a
		// bigger backup means the SQLite file was lost. Don't let the
		// scheduler bury the good backup under a near-empty one before an
		// operator had the chance to restore.
		if actor == "scheduler" && len(p.Users) <= 1 && lastUsers > len(p.Users) {
			u.logger.Warnf("backup skipped: database looks freshly initialised (%d user) but the newest backup has %d; restore it via POST /api/admin/backups/restore", len(p.Users), lastUsers)
			res.Skipped = true
			return res, nil
		}
	}

	data, suffix := plain, ".json"
	if u.cfg.Passphrase != "" {
		if data, err = encryptBackup(plain, u.cfg.Passphrase); err != nil {
			return nil, err
		}
		suffix += ".enc"
	}
	name := fmt.Sprintf("%s_%s_%d%s", time.Now().UTC().Format("20060102T150405.000Z"), hash, len(p.Users), suffix)
	if err := u.store.Put(ctx, name, data); err != nil {
		return nil, fmt.Errorf("write backup: %w", err)
	}
	res.Name = name
	u.prune(ctx)
	u.audit.Write(ctx, AuditEntry{Actor: actor, Action: ActionBackupCreated, Target: "backup:" + name, Success: true,
		Detail: map[string]any{"users": len(p.Users), "encrypted": u.cfg.Passphrase != "", "storage": u.store.Describe()}})
	return res, nil
}

// prune drops backups beyond cfg.Keep (best effort).
func (u *BackupUsecase) prune(ctx context.Context) {
	names, err := u.names(ctx)
	if err != nil || len(names) <= u.cfg.Keep {
		return
	}
	for _, n := range names[u.cfg.Keep:] {
		if err := u.store.Delete(ctx, n); err != nil {
			u.logger.Warnf("prune backup %s: %v", n, err)
		}
	}
}

// List returns stored backups, newest first.
func (u *BackupUsecase) List(ctx context.Context) ([]BackupInfo, error) {
	names, err := u.names(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BackupInfo, 0, len(names))
	for _, n := range names {
		ts, _, users, enc, _ := parseBackupName(n)
		out = append(out, BackupInfo{Name: n, CreatedAt: ts.Format(time.RFC3339), Users: users, Encrypted: enc})
	}
	return out, nil
}

// Restore merges a stored backup (by name, or the newest when empty) into
// the database. Passwords are never touched: restored users that did not
// exist get a random unusable password and are reported in NeedPassword so
// an admin can set one; existing users keep theirs. Roles and disabled
// flags are overwritten, missing permission patterns and namespaces are
// added, and nothing is deleted.
func (u *BackupUsecase) Restore(ctx context.Context, name, actor string) (*RestoreResult, error) {
	if name == "" {
		names, err := u.names(ctx)
		if err != nil {
			return nil, err
		}
		if len(names) == 0 {
			return nil, ErrBackupNotFound
		}
		name = names[0]
	}
	if _, _, _, _, ok := parseBackupName(name); !ok {
		return nil, ErrBackupNotFound
	}
	data, err := u.store.Get(ctx, name)
	if errors.Is(err, backupstore.ErrNotFound) {
		return nil, ErrBackupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read backup: %w", err)
	}
	if bytes.HasPrefix(data, []byte(backupMagic)) {
		if u.cfg.Passphrase == "" {
			return nil, ErrBackupNeedsPass
		}
		if data, err = decryptBackup(data, u.cfg.Passphrase); err != nil {
			return nil, err
		}
	}
	var p backupPayload
	if err := json.Unmarshal(data, &p); err != nil || p.Version != 1 {
		return nil, errors.New("backup: unsupported or corrupt payload")
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	res := &RestoreResult{NeedPassword: []string{}}
	for _, bu := range p.Users {
		usr, err := u.users.GetByUsername(ctx, bu.Username)
		switch {
		case errors.Is(err, ErrUserNotFound):
			hash, herr := unusablePasswordHash()
			if herr != nil {
				return res, herr
			}
			if usr, err = u.users.Create(ctx, bu.Username, hash, bu.Role); err != nil {
				return res, fmt.Errorf("restore user %s: %w", bu.Username, err)
			}
			res.UsersCreated++
			res.NeedPassword = append(res.NeedPassword, bu.Username)
		case err != nil:
			return res, err
		default:
			if usr.Role != bu.Role {
				if err := u.users.SetRole(ctx, usr.ID, bu.Role); err != nil {
					return res, err
				}
			}
			res.UsersUpdated++
		}
		if usr.Disabled != bu.Disabled {
			if err := u.users.SetDisabled(ctx, usr.ID, bu.Disabled); err != nil {
				return res, err
			}
		}
		for _, pat := range bu.Patterns {
			if _, err := u.perms.CreateOne(ctx, usr.ID, pat); err == nil {
				res.PermissionsAdded++
			} else if !errors.Is(err, ErrDuplicate) {
				return res, err
			}
		}
	}
	for _, n := range p.Namespaces {
		err := u.ns.Create(ctx, &Namespace{Path: n.Path, Description: n.Description, CreatedBy: n.CreatedBy})
		if err == nil {
			res.NamespacesAdded++
		} else if !errors.Is(err, ErrNamespaceExists) {
			return res, err
		}
	}
	u.audit.Write(ctx, AuditEntry{Actor: actor, Action: ActionBackupRestored, Target: "backup:" + name, Success: true,
		Detail: map[string]any{"users_created": res.UsersCreated, "users_updated": res.UsersUpdated,
			"permissions_added": res.PermissionsAdded, "namespaces_added": res.NamespacesAdded}})
	return res, nil
}

// unusablePasswordHash is a bcrypt hash of 32 random bytes nobody knows:
// the account exists but cannot log in until an admin sets a password.
func unusablePasswordHash() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(b)), bcrypt.DefaultCost)
	return string(h), err
}

// --- encryption: "DKB1" | salt(16) | nonce(12) | AES-256-GCM(plain) ---

func deriveBackupKey(pass string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(pass), salt, 1<<15, 8, 1, 32)
}

func encryptBackup(plain []byte, pass string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := deriveBackupKey(pass, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte(backupMagic), salt...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plain, nil), nil
}

func decryptBackup(data []byte, pass string) ([]byte, error) {
	hdr := len(backupMagic) + 16 + 12
	if len(data) < hdr {
		return nil, errors.New("backup: truncated")
	}
	salt := data[len(backupMagic) : len(backupMagic)+16]
	nonce := data[len(backupMagic)+16 : hdr]
	key, err := deriveBackupKey(pass, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, data[hdr:], nil)
	if err != nil {
		return nil, ErrBackupBadPass
	}
	return plain, nil
}
