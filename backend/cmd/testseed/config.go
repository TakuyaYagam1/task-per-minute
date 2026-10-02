package main

import (
	"errors"
	"strings"
)

const (
	enabledEnvName      = "TEST_ACCOUNTS_ENABLED"
	dsnEnvName          = "DB_DSN"
	expectedDBEnvName   = "TEST_ACCOUNTS_EXPECTED_DB"
	manifestPathEnvName = "TEST_ACCOUNTS_FILE"
	defaultManifestPath = "/seed/accounts.json"
)

var errInvalidConfiguration = errors.New("invalid test account configuration")

type config struct {
	dsn          string
	expectedDB   string
	manifestPath string
}

func loadConfig(lookup func(string) (string, bool)) (config, bool, error) {
	if lookup == nil {
		return config{}, false, errInvalidConfiguration
	}

	enabledValue, enabledSet := lookup(enabledEnvName)
	if !enabledSet || enabledValue == "" || enabledValue == "false" {
		return config{}, false, nil
	}
	if enabledValue != "true" {
		return config{}, false, errInvalidConfiguration
	}

	dsn, dsnSet := lookup(dsnEnvName)
	if !dsnSet || strings.TrimSpace(dsn) == "" {
		return config{}, false, errInvalidConfiguration
	}
	expectedDB, expectedSet := lookup(expectedDBEnvName)
	if !expectedSet || strings.TrimSpace(expectedDB) == "" {
		return config{}, false, errInvalidConfiguration
	}

	manifestPath, pathSet := lookup(manifestPathEnvName)
	if !pathSet || manifestPath == "" {
		manifestPath = defaultManifestPath
	}
	if strings.TrimSpace(manifestPath) == "" {
		return config{}, false, errInvalidConfiguration
	}

	return config{
		dsn:          dsn,
		expectedDB:   expectedDB,
		manifestPath: manifestPath,
	}, true, nil
}
