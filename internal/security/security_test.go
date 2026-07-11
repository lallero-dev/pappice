package security

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

func TestPasswordTokenAndSignatureHelpers(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password should be rejected")
	}
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !VerifyPassword(hash, "correct horse") {
		t.Fatal("password should verify")
	}
	if !strings.Contains(hash, "$120000$") {
		t.Fatalf("hash should use current iteration count: %q", hash)
	}
	if PasswordNeedsRehash(hash) {
		t.Fatal("fresh password hash should not need rehash")
	}
	legacyHash := hashPasswordWithIterations(t, "correct horse", 60000)
	if !VerifyPassword(legacyHash, "correct horse") {
		t.Fatal("legacy password hash should verify")
	}
	if !PasswordNeedsRehash(legacyHash) {
		t.Fatal("legacy password hash should need rehash")
	}
	if VerifyPassword(hash, "wrong horse") {
		t.Fatal("wrong password should not verify")
	}
	if VerifyPassword("not-a-password-hash", "correct horse") {
		t.Fatal("malformed hash should not verify")
	}
	if !PasswordNeedsRehash("not-a-password-hash") {
		t.Fatal("malformed hash should need rehash")
	}

	token, err := RandomToken()
	if err != nil {
		t.Fatalf("random token: %v", err)
	}
	rawToken, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(rawToken) != 32 {
		t.Fatalf("random token = %q, decoded bytes = %d, err = %v", token, len(rawToken), err)
	}
	if got, want := HashToken("token"), "3c469e9d6c5875d37a43f353d4f88e61fcf812c66eee3457465a40b0da4153e0"; got != want {
		t.Fatalf("token hash = %q, want %q", got, want)
	}
	if !ConstantTimeEqual("same", "same") || ConstantTimeEqual("same", "different") {
		t.Fatal("constant-time equality returned unexpected result")
	}
	if got, want := HMACSHA256("secret", []byte("payload")), "b82fcb791acec57859b989b430a826488ce2e479fdf92326bd0a2e8375a42ba4"; got != want {
		t.Fatalf("HMAC = %q, want %q", got, want)
	}
}

func hashPasswordWithIterations(t *testing.T, password string, iterations int) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, passwordKeyBytes)
	if err != nil {
		t.Fatal(err)
	}
	return passwordAlgorithm + "$" +
		strconv.Itoa(iterations) + "$" +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(key)
}
