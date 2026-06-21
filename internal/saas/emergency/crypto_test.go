package emergency

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptBytes(t *testing.T) {
	plain := []byte(`{"hello":"world"}`)
	encrypted, err := EncryptBytes(plain, "test-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted.Ciphertext == "" || bytes.Contains([]byte(encrypted.Ciphertext), plain) {
		t.Fatalf("ciphertext was not encrypted")
	}
	decrypted, err := DecryptBytes(encrypted, "test-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("decrypted = %q, want %q", decrypted, plain)
	}
}

func TestDecryptRejectsWrongPassphrase(t *testing.T) {
	encrypted, err := EncryptBytes([]byte("secret"), "right")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptBytes(encrypted, "wrong"); err == nil {
		t.Fatal("expected wrong passphrase to fail")
	}
}
