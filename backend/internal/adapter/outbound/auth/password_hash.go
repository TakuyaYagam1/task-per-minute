package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	playerPasswordMemoryKiB   uint32 = 19 * 1024
	playerPasswordTime        uint32 = 2
	playerPasswordThreads     uint8  = 1
	playerPasswordSaltSize           = 16
	playerPasswordHashSize           = 32
	playerPasswordHashVersion        = 19
	playerPasswordEncodedMax         = 256
)

var ErrMalformedPlayerPasswordHash = errors.New("malformed player password hash")

type PasswordHasher struct{}

func NewPasswordHasher() *PasswordHasher {
	return &PasswordHasher{}
}

func (*PasswordHasher) Hash(password string) (string, error) {
	salt := make([]byte, playerPasswordSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate player password salt: %w", err)
	}
	hash := argon2.IDKey(
		[]byte(password),
		salt,
		playerPasswordTime,
		playerPasswordMemoryKiB,
		playerPasswordThreads,
		playerPasswordHashSize,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		playerPasswordHashVersion,
		playerPasswordMemoryKiB,
		playerPasswordTime,
		playerPasswordThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func (*PasswordHasher) Verify(password, encodedHash string) (bool, error) {
	params, salt, expected, err := parsePlayerPasswordHash(encodedHash)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrMalformedPlayerPasswordHash, err)
	}
	actual := argon2.IDKey(
		[]byte(password),
		salt,
		params.time,
		params.memory,
		params.threads,
		playerPasswordHashSize,
	)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

type playerPasswordParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

type rawPlayerPasswordParams struct {
	memory  string
	time    string
	threads string
}

func parsePlayerPasswordHash(encoded string) (playerPasswordParams, []byte, []byte, error) {
	if len(encoded) == 0 || len(encoded) > playerPasswordEncodedMax {
		return playerPasswordParams{}, nil, nil, fmt.Errorf("invalid hash length")
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return playerPasswordParams{}, nil, nil, fmt.Errorf("invalid hash format")
	}
	if parts[2] != fmt.Sprintf("v=%d", playerPasswordHashVersion) {
		return playerPasswordParams{}, nil, nil, fmt.Errorf("unsupported hash version")
	}
	params, err := parsePlayerPasswordParams(parts[3])
	if err != nil {
		return playerPasswordParams{}, nil, nil, err
	}
	salt, err := decodePlayerPasswordPart(parts[4], playerPasswordSaltSize, "salt")
	if err != nil {
		return playerPasswordParams{}, nil, nil, err
	}
	digest, err := decodePlayerPasswordPart(parts[5], playerPasswordHashSize, "digest")
	if err != nil {
		return playerPasswordParams{}, nil, nil, err
	}
	return params, salt, digest, nil
}

func parsePlayerPasswordParams(encoded string) (playerPasswordParams, error) {
	items := strings.Split(encoded, ",")
	if len(items) != 3 {
		return playerPasswordParams{}, fmt.Errorf("invalid hash parameters")
	}
	var values rawPlayerPasswordParams
	for _, item := range items {
		key, value, err := parsePlayerPasswordParameter(item)
		if err != nil {
			return playerPasswordParams{}, err
		}
		if err := values.set(key, value); err != nil {
			return playerPasswordParams{}, err
		}
	}
	return values.toParams()
}

func parsePlayerPasswordParameter(item string) (string, string, error) {
	key, value, ok := strings.Cut(item, "=")
	if !ok || key == "" || value == "" {
		return "", "", fmt.Errorf("invalid hash parameter")
	}
	return key, value, nil
}

func (p *rawPlayerPasswordParams) set(key, value string) error {
	switch key {
	case "m":
		if p.memory != "" {
			return fmt.Errorf("duplicate memory parameter")
		}
		p.memory = value
	case "t":
		if p.time != "" {
			return fmt.Errorf("duplicate time parameter")
		}
		p.time = value
	case "p":
		if p.threads != "" {
			return fmt.Errorf("duplicate parallelism parameter")
		}
		p.threads = value
	default:
		return fmt.Errorf("unknown hash parameter")
	}
	return nil
}

func (p rawPlayerPasswordParams) toParams() (playerPasswordParams, error) {
	if p.memory != strconv.FormatUint(uint64(playerPasswordMemoryKiB), 10) ||
		p.time != strconv.FormatUint(uint64(playerPasswordTime), 10) ||
		p.threads != strconv.FormatUint(uint64(playerPasswordThreads), 10) {
		return playerPasswordParams{}, fmt.Errorf("unsupported hash parameters")
	}
	return playerPasswordParams{
		memory:  playerPasswordMemoryKiB,
		time:    playerPasswordTime,
		threads: playerPasswordThreads,
	}, nil
}

func decodePlayerPasswordPart(encoded string, expectedLength int, partName string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid hash %s encoding", partName)
	}
	if len(decoded) != expectedLength {
		return nil, fmt.Errorf("invalid hash %s length", partName)
	}
	return decoded, nil
}
