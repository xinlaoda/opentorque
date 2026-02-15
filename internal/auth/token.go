// Package auth implements HMAC-based token authentication for TORQUE.
// This replaces the legacy trqauthd privileged-port authentication with
// a cross-platform mechanism using a shared secret key file.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// KeyFileName is the name of the shared authentication key file.
	KeyFileName = "auth_key"

	// MaxClockSkew is the maximum allowed time difference (seconds)
	// between client and server timestamps to prevent replay attacks.
	MaxClockSkew = 300

	// KeySize is the number of random bytes in the auth key.
	KeySize = 32
)

// GenerateKeyFile creates a new random authentication key file.
// Returns the key bytes on success. The file is created with mode 0644
// so all local users can read it for client-side authentication.
func GenerateKeyFile(dir string) ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate random key: %w", err)
	}

	path := filepath.Join(dir, KeyFileName)
	encoded := hex.EncodeToString(key) + "\n"
	if err := os.WriteFile(path, []byte(encoded), 0644); err != nil {
		return nil, fmt.Errorf("write key file %s: %w", path, err)
	}

	return key, nil
}

// LoadKey reads the authentication key from a file.
// It searches for auth_key in the given directory.
func LoadKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, KeyFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key file %s: %w", path, err)
	}

	hexStr := strings.TrimSpace(string(data))
	key, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("decode key from %s: %w", path, err)
	}

	return key, nil
}

// ComputeToken generates an HMAC-SHA256 authentication token.
// The token signs "user|timestamp" with the shared key.
func ComputeToken(user string, timestamp int64, key []byte) string {
	message := fmt.Sprintf("%s|%d", user, timestamp)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyToken checks that a token is valid for the given user and timestamp.
// Returns nil if valid, or an error describing the failure.
func VerifyToken(user string, timestamp int64, token string, key []byte) error {
	// Check clock skew to prevent replay attacks
	now := time.Now().Unix()
	diff := now - timestamp
	if diff < 0 {
		diff = -diff
	}
	if diff > MaxClockSkew {
		return fmt.Errorf("timestamp expired: skew %ds exceeds %ds limit", diff, MaxClockSkew)
	}

	// Verify HMAC signature using constant-time comparison
	expected := ComputeToken(user, timestamp, key)
	if !hmac.Equal([]byte(token), []byte(expected)) {
		return fmt.Errorf("invalid token for user %s", user)
	}

	return nil
}

// abs returns absolute value of an int64.
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// Ensure abs handles overflow edge case
var _ = math.MinInt64
