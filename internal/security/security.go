package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const (
	passwordAlgorithm  = "pbkdf2-sha256"
	passwordIterations = 120000

	maximumPasswordIterations = 2000000
	passwordSaltBytes         = 16
	passwordKeyBytes          = 32
)

type passwordHashParams struct {
	iterations int
	salt       []byte
	expected   []byte
}

func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", fmt.Errorf("password must be at least 8 characters")
	}
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := pbkdf2SHA256([]byte(password), salt, passwordIterations, passwordKeyBytes)
	return fmt.Sprintf("%s$%d$%s$%s",
		passwordAlgorithm,
		passwordIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func VerifyPassword(encoded, password string) bool {
	params, ok := parsePasswordHash(encoded)
	if !ok {
		return false
	}
	actual := pbkdf2SHA256([]byte(password), params.salt, params.iterations, len(params.expected))
	return subtle.ConstantTimeCompare(actual, params.expected) == 1
}

func PasswordNeedsRehash(encoded string) bool {
	params, ok := parsePasswordHash(encoded)
	if !ok {
		return true
	}
	return params.iterations < passwordIterations ||
		len(params.salt) < passwordSaltBytes ||
		len(params.expected) < passwordKeyBytes
}

func parsePasswordHash(encoded string) (passwordHashParams, bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != passwordAlgorithm {
		return passwordHashParams{}, false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 || iterations > maximumPasswordIterations {
		return passwordHashParams{}, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return passwordHashParams{}, false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(expected) == 0 {
		return passwordHashParams{}, false
	}
	return passwordHashParams{iterations: iterations, salt: salt, expected: expected}, true
}

func RandomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func HMACSHA256(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
	const hashLen = 32
	blockCount := (keyLen + hashLen - 1) / hashLen
	key := make([]byte, 0, blockCount*hashLen)

	mac := hmac.New(sha256.New, password)

	for block := 1; block <= blockCount; block++ {
		mac.Reset()
		mac.Write(salt)
		var counter [4]byte
		binary.BigEndian.PutUint32(counter[:], uint32(block))
		mac.Write(counter[:])
		u := mac.Sum(nil)
		t := make([]byte, hashLen)
		copy(t, u)

		for i := 1; i < iterations; i++ {
			mac.Reset()
			mac.Write(u)
			u = mac.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		key = append(key, t...)
	}
	return key[:keyLen]
}
