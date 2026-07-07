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
	if token == "" || HashToken(token) == HashToken(token+"x") {
		t.Fatalf("token/hash helpers failed token=%q", token)
	}
	if !ConstantTimeEqual("same", "same") || ConstantTimeEqual("same", "different") {
		t.Fatal("constant-time equality returned unexpected result")
	}
	if got := HMACSHA256("secret", []byte("payload")); got == "" || got == HMACSHA256("secret", []byte("other")) {
		t.Fatalf("unexpected hmac value %q", got)
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
