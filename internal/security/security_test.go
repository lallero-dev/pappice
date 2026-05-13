package security

import "testing"

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
	if VerifyPassword(hash, "wrong horse") {
		t.Fatal("wrong password should not verify")
	}
	if VerifyPassword("not-a-password-hash", "correct horse") {
		t.Fatal("malformed hash should not verify")
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
