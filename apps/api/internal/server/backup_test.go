package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackup_RunRestoreKeepsPasswordsOut(t *testing.T) {
	h := newHarness(t)
	defer h.stop()
	ctx := context.Background()

	if err := h.users.EnsureAdmin(ctx, "admin", "a-strong-password-42"); err != nil {
		t.Fatal(err)
	}
	bob, err := h.users.Create(ctx, "bob", "bob-password-1234", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.perms.GrantPatterns(ctx, bob.ID, []string{"acme/**"}); err != nil {
		t.Fatal(err)
	}
	bobHash := bob.PasswordHash

	res, err := h.backup.Run(ctx, "test")
	if err != nil || res.Skipped || res.Users != 2 {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if again, _ := h.backup.Run(ctx, "test"); !again.Skipped {
		t.Fatal("unchanged state must be skipped")
	}
	list, err := h.backup.List(ctx)
	if err != nil || len(list) != 1 || list[0].Users != 2 {
		t.Fatalf("List = %+v, %v", list, err)
	}

	// The stored object must not contain any password material.
	entries, _ := os.ReadDir(h.backupDir)
	raw, _ := os.ReadFile(filepath.Join(h.backupDir, entries[0].Name()))
	for _, bad := range []string{"password", "$2a$", bobHash} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("backup contains %q:\n%s", bad, raw)
		}
	}

	// Existing user: restore must leave the password alone.
	if _, err := h.backup.Restore(ctx, "", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.users.VerifyCredentials(ctx, "bob", "bob-password-1234"); err != nil {
		t.Fatalf("existing user's password must survive restore: %v", err)
	}

	// Lost user: recreated with role + grants, but cannot log in until an
	// admin sets a password.
	old, _ := h.perms.ListForUser(ctx, bob.ID)
	for _, pm := range old {
		_ = h.perms.Revoke(ctx, pm.ID)
	}
	if err := h.users.Delete(ctx, bob.ID); err != nil {
		t.Fatal(err)
	}
	rr, err := h.backup.Restore(ctx, "", "test")
	if err != nil || rr.UsersCreated != 1 || rr.PermissionsAdded != 1 || len(rr.NeedPassword) != 1 || rr.NeedPassword[0] != "bob" {
		t.Fatalf("Restore = %+v, %v", rr, err)
	}
	if _, err := h.users.VerifyCredentials(ctx, "bob", "bob-password-1234"); err == nil {
		t.Fatal("restored user must not be able to log in with the old password")
	}
	restored, _ := h.users.GetByUsername(ctx, "bob")
	if restored.Role != "write" {
		t.Fatalf("role = %q", restored.Role)
	}
	pats, _ := h.perms.ListForUser(ctx, restored.ID)
	if len(pats) != 1 || pats[0].RepoPattern != "acme/**" {
		t.Fatalf("permissions after restore: %+v", pats)
	}
	// Admin sets a new password → works.
	if err := h.users.SetPassword(ctx, restored.ID, "fresh-password-77"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.users.VerifyCredentials(ctx, "bob", "fresh-password-77"); err != nil {
		t.Fatalf("login after reset: %v", err)
	}
}

func TestBackup_SchedulerWontOverwriteWithFreshDatabase(t *testing.T) {
	h := newHarness(t)
	defer h.stop()
	ctx := context.Background()
	_ = h.users.EnsureAdmin(ctx, "admin", "a-strong-password-42")
	_, _ = h.users.Create(ctx, "bob", "bob-password-1234", "view")
	if _, err := h.backup.Run(ctx, "test"); err != nil {
		t.Fatal(err)
	}

	// Same storage, brand-new database holding only the bootstrap admin.
	h2 := newHarnessWithBackupDir(t, h.backupDir)
	defer h2.stop()
	_ = h2.users.EnsureAdmin(ctx, "admin", "another-password-1")
	res, err := h2.backup.Run(ctx, "scheduler")
	if err != nil || !res.Skipped {
		t.Fatalf("scheduler must skip: %+v %v", res, err)
	}
	if _, err := h2.backup.Restore(ctx, "", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := h2.users.GetByUsername(ctx, "bob"); err != nil {
		t.Fatalf("bob after restore into new db: %v", err)
	}
}
