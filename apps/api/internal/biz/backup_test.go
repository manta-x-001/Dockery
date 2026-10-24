package biz

import (
	"bytes"
	"testing"
)

func TestBackupEncryptionRoundTrip(t *testing.T) {
	plain := []byte(`{"version":1,"users":[]}`)
	enc, err := encryptBackup(plain, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, plain) || !bytes.HasPrefix(enc, []byte(backupMagic)) {
		t.Fatal("ciphertext must not contain the plaintext")
	}
	got, err := decryptBackup(enc, "correct horse")
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("decrypt = %q, %v", got, err)
	}
	if _, err := decryptBackup(enc, "wrong"); err == nil {
		t.Fatal("wrong passphrase must fail")
	}
}
