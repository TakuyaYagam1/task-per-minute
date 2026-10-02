package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestNewManifestHasExpectedUniqueAccounts(t *testing.T) {
	value, err := newManifest()
	if err != nil {
		t.Fatal("manifest creation failed")
	}
	if len(value.Accounts) != accountCount || value.Applied {
		t.Fatal("manifest metadata was invalid")
	}
	if err := validateManifest(value); err != nil {
		t.Fatal("generated manifest failed validation")
	}

	passwords := make(map[string]struct{}, accountCount)
	identifiers := make(map[uuid.UUID]struct{}, accountCount*2)
	for index, account := range value.Accounts {
		username := accountUsername(index + 1)
		if account.Username != username || account.Email != username+"@example.invalid" {
			t.Fatal("manifest identity did not match the test account list")
		}
		if _, exists := passwords[account.Password]; exists {
			t.Fatal("generated account passwords were not unique")
		}
		passwords[account.Password] = struct{}{}
		for _, encoded := range []string{account.PlayerID, account.AccountID} {
			identifier, err := uuid.Parse(encoded)
			if err != nil {
				t.Fatal("manifest identity was not a UUID")
			}
			if _, exists := identifiers[identifier]; exists {
				t.Fatal("manifest UUIDs were not unique")
			}
			identifiers[identifier] = struct{}{}
		}
	}
}

func TestManifestCreationIsPrivateAndStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	first, err := loadOrCreateManifest(path)
	if err != nil {
		t.Fatal("manifest could not be created")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != manifestMode {
		t.Fatal("manifest permissions were not private")
	}
	second, err := loadOrCreateManifest(path)
	if err != nil {
		t.Fatal("existing manifest could not be read")
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("manifest credentials or identities changed on reload")
	}
}

func TestConcurrentManifestCreationKeepsOneIdentitySet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	const writers = 8
	values := make(chan manifest, writers)
	errorsFound := make(chan error, writers)
	var wait sync.WaitGroup
	wait.Add(writers)
	for range writers {
		go func() {
			defer wait.Done()
			value, err := loadOrCreateManifest(path)
			if err != nil {
				errorsFound <- err
				return
			}
			values <- value
		}()
	}
	wait.Wait()
	close(values)
	close(errorsFound)
	for range errorsFound {
		t.Fatal("concurrent manifest creation failed")
	}

	var expected *manifest
	for value := range values {
		if expected == nil {
			copyOfValue := value
			expected = &copyOfValue
			continue
		}
		if !reflect.DeepEqual(*expected, value) {
			t.Fatal("concurrent writers returned different identity sets")
		}
	}
	if expected == nil {
		t.Fatal("concurrent manifest creation returned no result")
	}
}

func TestManifestUpdateKeepsPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	value, err := loadOrCreateManifest(path)
	if err != nil {
		t.Fatal("manifest could not be created")
	}
	value.Applied = true
	if err := writeManifest(path, value, false); err != nil {
		t.Fatal("manifest status could not be updated")
	}
	loaded, err := readManifest(path)
	if err != nil || !loaded.Applied {
		t.Fatal("manifest status was not persisted")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != manifestMode {
		t.Fatal("updated manifest permissions were not private")
	}
}

func TestRefreshManifestUsesAppliedStateFromPriorRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	loaded, err := loadOrCreateManifest(path)
	if err != nil {
		t.Fatal("manifest could not be created")
	}
	current := loaded
	current.Applied = true
	if err := writeManifest(path, current, false); err != nil {
		t.Fatal("manifest status could not be updated")
	}
	if err := refreshManifest(path, &loaded); err != nil || !loaded.Applied {
		t.Fatal("stale manifest state was not refreshed")
	}
}

func TestRefreshManifestRejectsCredentialChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	loaded, err := loadOrCreateManifest(path)
	if err != nil {
		t.Fatal("manifest could not be created")
	}
	current := loaded
	current.Accounts = append([]manifestAccount(nil), loaded.Accounts...)
	current.Accounts[0].AccountID = uuid.NewString()
	if err := writeManifest(path, current, false); err != nil {
		t.Fatal("manifest fixture could not be changed")
	}
	if !errors.Is(refreshManifest(path, &loaded), errInvalidManifest) {
		t.Fatal("changed manifest identity was accepted")
	}
}

func TestManifestRejectsSymlinkAndLoosePermissions(t *testing.T) {
	directory := t.TempDir()
	validPath := filepath.Join(directory, "valid.json")
	if _, err := loadOrCreateManifest(validPath); err != nil {
		t.Fatal("valid manifest could not be created")
	}
	symlinkPath := filepath.Join(directory, "linked.json")
	if err := os.Symlink(validPath, symlinkPath); err != nil {
		t.Fatal("symlink fixture could not be created")
	}
	if _, err := readManifest(symlinkPath); !errors.Is(err, errManifestFile) {
		t.Fatal("manifest symlink was accepted")
	}

	if err := os.Chmod(validPath, 0o644); err != nil {
		t.Fatal("permission fixture could not be changed")
	}
	if _, err := readManifest(validPath); !errors.Is(err, errManifestFile) {
		t.Fatal("group-readable manifest was accepted")
	}
}

func TestManifestRejectsIdentityAndCredentialChanges(t *testing.T) {
	value, err := newManifest()
	if err != nil {
		t.Fatal("manifest creation failed")
	}
	wrongEmail := value
	wrongEmail.Accounts = append([]manifestAccount(nil), value.Accounts...)
	wrongEmail.Accounts[0].Email = "other@example.invalid"
	if !errors.Is(validateManifest(wrongEmail), errInvalidManifest) {
		t.Fatal("unexpected email identity was accepted")
	}

	duplicateID := value
	duplicateID.Accounts = append([]manifestAccount(nil), value.Accounts...)
	duplicateID.Accounts[1].AccountID = duplicateID.Accounts[0].PlayerID
	if !errors.Is(validateManifest(duplicateID), errInvalidManifest) {
		t.Fatal("duplicate UUID was accepted")
	}

	tweakPassword := value
	tweakPassword.Accounts = append([]manifestAccount(nil), value.Accounts...)
	tweakPassword.Accounts[0].Password = "weak"
	if !errors.Is(validateManifest(tweakPassword), errInvalidManifest) {
		t.Fatal("invalid password material was accepted")
	}
}
