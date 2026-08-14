package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("unexpected password format: %q", encoded)
	}
	if !VerifyPassword(encoded, "correct horse battery staple") {
		t.Fatal("correct password was rejected")
	}
	if VerifyPassword(encoded, "wrong password") {
		t.Fatal("wrong password was accepted")
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("too-short"); err == nil {
		t.Fatal("short password was accepted")
	}
	if err := ValidatePassword(strings.Repeat("x", 1025)); err == nil {
		t.Fatal("oversized password was accepted")
	}
}

func TestVerifyPasswordRejectsUnsafeParameters(t *testing.T) {
	encoded := "$argon2id$v=19$m=999999,t=3,p=2$c2FsdA$aGFzaA"
	if VerifyPassword(encoded, "anything") {
		t.Fatal("unsafe password parameters were accepted")
	}
}
