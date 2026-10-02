package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	manifestVersion = 1
	accountCount    = 16
	manifestMode    = 0o600
	maxManifestSize = 64 * 1024
)

var (
	errManifestNotFound     = errors.New("test account manifest does not exist")
	errManifestExists       = errors.New("test account manifest already exists")
	errManifestFile         = errors.New("test account manifest file is invalid")
	errInvalidManifest      = errors.New("test account manifest content is invalid")
	errCredentialGeneration = errors.New("test account credential generation failed")
)

type manifest struct {
	Version  int               `json:"version"`
	Applied  bool              `json:"applied"`
	Accounts []manifestAccount `json:"accounts"`
}

type manifestAccount struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	PlayerID  string `json:"player_id"`
	AccountID string `json:"account_id"`
}

func loadOrCreateManifest(path string) (manifest, error) {
	current, err := readManifest(path)
	if err == nil {
		return current, nil
	}
	if !errors.Is(err, errManifestNotFound) {
		return manifest{}, err
	}

	created, err := newManifest()
	if err != nil {
		return manifest{}, err
	}
	if err := writeManifest(path, created, true); err != nil {
		if errors.Is(err, errManifestExists) {
			return readManifest(path)
		}
		return manifest{}, err
	}
	return created, nil
}

func newManifest() (manifest, error) {
	created := manifest{
		Version:  manifestVersion,
		Accounts: make([]manifestAccount, 0, accountCount),
	}
	for index := 1; index <= accountCount; index++ {
		password, err := newPassword()
		if err != nil {
			return manifest{}, errCredentialGeneration
		}
		playerID, err := uuid.NewRandom()
		if err != nil {
			return manifest{}, errCredentialGeneration
		}
		accountID, err := uuid.NewRandom()
		if err != nil {
			return manifest{}, errCredentialGeneration
		}
		username := accountUsername(index)
		created.Accounts = append(created.Accounts, manifestAccount{
			Username:  username,
			Email:     username + "@example.invalid",
			Password:  password,
			PlayerID:  playerID.String(),
			AccountID: accountID.String(),
		})
	}
	if err := validateManifest(created); err != nil {
		return manifest{}, err
	}
	return created, nil
}

func newPassword() (string, error) {
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return "", errCredentialGeneration
	}
	return "TpM-7" + base64.RawURLEncoding.EncodeToString(secret), nil
}

func accountUsername(index int) string {
	return fmt.Sprintf("demo%02d", index)
}

func readManifest(path string) (value manifest, result error) {
	file, err := openPrivateManifest(path)
	if err != nil {
		return manifest{}, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && result == nil {
			result = errManifestFile
		}
	}()

	data, err := io.ReadAll(io.LimitReader(file, maxManifestSize+1))
	if err != nil || len(data) > maxManifestSize {
		return manifest{}, errManifestFile
	}
	return decodeManifest(data)
}

func openPrivateManifest(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errManifestNotFound
	}
	if err != nil || !privateManifestInfo(info) {
		return nil, errManifestFile
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, errManifestFile
	}
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) || !privateManifestInfo(openedInfo) {
		if closeErr := file.Close(); closeErr != nil {
			return nil, errManifestFile
		}
		return nil, errManifestFile
	}
	return file, nil
}

func privateManifestInfo(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm() == manifestMode && info.Size() <= maxManifestSize
}

func decodeManifest(data []byte) (manifest, error) {
	var value manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return manifest{}, errInvalidManifest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return manifest{}, errInvalidManifest
	}
	if err := validateManifest(value); err != nil {
		return manifest{}, errInvalidManifest
	}
	return value, nil
}

func validateManifest(value manifest) error {
	if value.Version != manifestVersion || len(value.Accounts) != accountCount {
		return errInvalidManifest
	}

	identifiers := make(map[uuid.UUID]struct{}, accountCount*2)
	passwords := make(map[string]struct{}, accountCount)
	for index, account := range value.Accounts {
		username := accountUsername(index + 1)
		if account.Username != username || account.Email != username+"@example.invalid" || !validGeneratedPassword(account.Password) {
			return errInvalidManifest
		}
		if _, exists := passwords[account.Password]; exists {
			return errInvalidManifest
		}
		passwords[account.Password] = struct{}{}

		for _, identifier := range []string{account.PlayerID, account.AccountID} {
			parsed, err := uuid.Parse(identifier)
			if err != nil || parsed.String() != identifier {
				return errInvalidManifest
			}
			if _, exists := identifiers[parsed]; exists {
				return errInvalidManifest
			}
			identifiers[parsed] = struct{}{}
		}
	}
	return nil
}

func validGeneratedPassword(password string) bool {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 20 || utf8.RuneCountInString(password) > 128 {
		return false
	}
	var hasLower, hasUpper, hasDigit, hasPunctuationOrSymbol bool
	for _, character := range password {
		hasLower = hasLower || unicode.IsLower(character)
		hasUpper = hasUpper || unicode.IsUpper(character)
		hasDigit = hasDigit || unicode.IsDigit(character)
		hasPunctuationOrSymbol = hasPunctuationOrSymbol || unicode.IsPunct(character) || unicode.IsSymbol(character)
	}
	return hasLower && hasUpper && hasDigit && hasPunctuationOrSymbol
}

func writeManifest(path string, value manifest, createOnly bool) error {
	data, err := encodeManifest(value)
	if err != nil {
		return err
	}
	if !createOnly {
		if err := validateManifestTarget(path); err != nil {
			return err
		}
	}
	return writeManifestFile(path, data, createOnly)
}

func encodeManifest(value manifest) ([]byte, error) {
	if err := validateManifest(value); err != nil {
		return nil, errInvalidManifest
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, errInvalidManifest
	}
	return append(data, '\n'), nil
}

func validateManifestTarget(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !privateManifestInfo(info) {
		return errManifestFile
	}
	return nil
}

func writeManifestFile(path string, data []byte, createOnly bool) error {
	directory := filepath.Dir(path)
	temporaryPath, err := writeTemporaryManifest(directory, filepath.Base(path), data)
	if err != nil {
		return err
	}
	defer removeTemporaryManifest(&temporaryPath)

	if createOnly {
		if err := os.Link(temporaryPath, path); err != nil {
			if errors.Is(err, os.ErrExist) {
				return errManifestExists
			}
			return errManifestFile
		}
		if err := os.Remove(temporaryPath); err != nil {
			return errManifestFile
		}
		temporaryPath = ""
	} else if err := os.Rename(temporaryPath, path); err != nil {
		return errManifestFile
	} else {
		temporaryPath = ""
	}
	if err := syncDirectory(directory); err != nil {
		return errManifestFile
	}
	return nil
}

func writeTemporaryManifest(directory, baseName string, data []byte) (string, error) {
	temporary, err := os.CreateTemp(directory, "."+baseName+".tmp-")
	if err != nil {
		return "", errManifestFile
	}
	temporaryPath := temporary.Name()
	if err := temporary.Chmod(manifestMode); err != nil {
		cleanupTemporaryManifest(temporary, temporaryPath)
		return "", errManifestFile
	}
	written, writeErr := temporary.Write(data)
	if writeErr != nil || written != len(data) {
		cleanupTemporaryManifest(temporary, temporaryPath)
		return "", errManifestFile
	}
	if err := temporary.Sync(); err != nil {
		cleanupTemporaryManifest(temporary, temporaryPath)
		return "", errManifestFile
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return "", errManifestFile
	}
	return temporaryPath, nil
}

func cleanupTemporaryManifest(file *os.File, path string) {
	_ = file.Close()
	_ = os.Remove(path)
}

func removeTemporaryManifest(path *string) {
	if *path != "" {
		_ = os.Remove(*path)
	}
	*path = ""
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return errManifestFile
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return errManifestFile
	}
	if err := directory.Close(); err != nil {
		return errManifestFile
	}
	return nil
}
