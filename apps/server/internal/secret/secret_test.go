package secret

import (
	"bytes"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	box, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	a, err := box.Encrypt([]byte("top-secret"), "project:1:DB_URL")
	if err != nil {
		t.Fatal(err)
	}
	b, err := box.Encrypt([]byte("top-secret"), "project:1:DB_URL")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("ciphertexts reused a nonce")
	}
	plain, err := box.Decrypt(a, "project:1:DB_URL")
	if err != nil || string(plain) != "top-secret" {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
	if _, err := box.Decrypt(a, "project:2:DB_URL"); err == nil {
		t.Fatal("ciphertext accepted wrong context")
	}
}

func TestUnavailable(t *testing.T) {
	box, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := box.Encrypt([]byte("x"), "ctx"); err != ErrUnavailable {
		t.Fatalf("got %v", err)
	}
}
