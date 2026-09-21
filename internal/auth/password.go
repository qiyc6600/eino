package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	passwordHashMemory      uint32 = 19 * 1024
	passwordHashIterations  uint32 = 2
	passwordHashParallelism uint8  = 1
	passwordHashSaltLength         = 16
	passwordHashKeyLength   uint32 = 32
)

var errInvalidPasswordHash = errors.New("invalid password hash")

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordHashSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, passwordHashIterations, passwordHashMemory, passwordHashParallelism, passwordHashKeyLength)
	encoding := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, passwordHashMemory, passwordHashIterations, passwordHashParallelism,
		encoding.EncodeToString(salt), encoding.EncodeToString(hash)), nil
}

// verifyPassword supports legacy unsalted SHA-256 hashes so successful logins
// can upgrade existing accounts without a forced password reset.
func verifyPassword(encoded, password string) (valid, needsUpgrade bool, err error) {
	if isLegacySHA256Hash(encoded) {
		expected, _ := hex.DecodeString(encoded)
		actual := sha256.Sum256([]byte(password))
		return subtle.ConstantTimeCompare(expected, actual[:]) == 1, true, nil
	}

	params, salt, expected, err := parseArgon2IDHash(encoded)
	if err != nil {
		return false, false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, uint32(len(expected)))
	valid = subtle.ConstantTimeCompare(expected, actual) == 1
	needsUpgrade = valid && (params.memory != passwordHashMemory ||
		params.iterations != passwordHashIterations || params.parallelism != passwordHashParallelism ||
		uint32(len(expected)) != passwordHashKeyLength || len(salt) != passwordHashSaltLength)
	return valid, needsUpgrade, nil
}

func isLegacySHA256Hash(encoded string) bool {
	if len(encoded) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

type argon2IDParams struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parseArgon2IDHash(encoded string) (argon2IDParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return argon2IDParams{}, nil, nil, errInvalidPasswordHash
	}
	parameterParts := strings.Split(parts[3], ",")
	if len(parameterParts) != 3 {
		return argon2IDParams{}, nil, nil, errInvalidPasswordHash
	}
	memory, err := parsePasswordHashParameter(parameterParts[0], "m=", 32)
	if err != nil {
		return argon2IDParams{}, nil, nil, errInvalidPasswordHash
	}
	iterations, err := parsePasswordHashParameter(parameterParts[1], "t=", 32)
	if err != nil {
		return argon2IDParams{}, nil, nil, errInvalidPasswordHash
	}
	parallelism, err := parsePasswordHashParameter(parameterParts[2], "p=", 8)
	if err != nil {
		return argon2IDParams{}, nil, nil, errInvalidPasswordHash
	}
	params := argon2IDParams{memory: uint32(memory), iterations: uint32(iterations), parallelism: uint8(parallelism)}
	// Bound values before invoking Argon2 so a malformed database value cannot
	// force excessive CPU or memory allocation during login.
	if params.memory < 8*1024 || params.memory > 256*1024 || params.iterations == 0 || params.iterations > 10 || params.parallelism == 0 || params.parallelism > 16 {
		return argon2IDParams{}, nil, nil, errInvalidPasswordHash
	}
	encoding := base64.RawStdEncoding
	salt, err := encoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return argon2IDParams{}, nil, nil, errInvalidPasswordHash
	}
	hash, err := encoding.DecodeString(parts[5])
	if err != nil || len(hash) < 16 || len(hash) > 64 {
		return argon2IDParams{}, nil, nil, errInvalidPasswordHash
	}
	return params, salt, hash, nil
}

func parsePasswordHashParameter(value, prefix string, bitSize int) (uint64, error) {
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return 0, errInvalidPasswordHash
	}
	return strconv.ParseUint(value[len(prefix):], 10, bitSize)
}

func legacyPasswordHash(password string) string {
	hash := sha256.Sum256([]byte(password))
	return hex.EncodeToString(hash[:])
}
