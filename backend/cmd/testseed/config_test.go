package main

import (
	"context"
	"errors"
	"testing"
)

func TestLoadConfigDisabledWithoutDatabaseSettings(t *testing.T) {
	for _, values := range []map[string]string{
		{},
		{enabledEnvName: "false"},
		{enabledEnvName: ""},
	} {
		loaded, enabled, err := loadConfig(mapLookup(values))
		if err != nil || enabled || loaded != (config{}) {
			t.Fatal("disabled configuration was not ignored")
		}
	}
}

func TestLoadConfigRequiresExplicitDatabaseGuards(t *testing.T) {
	base := map[string]string{enabledEnvName: "true"}
	for _, test := range []struct {
		name   string
		values map[string]string
	}{
		{name: "missing dsn", values: cloneValues(base)},
		{name: "missing expected database", values: withValue(base, dsnEnvName, "postgres://local")},
		{name: "blank dsn", values: withValue(withValue(base, dsnEnvName, " "), expectedDBEnvName, "seed_tests")},
		{name: "blank expected database", values: withValue(withValue(base, dsnEnvName, "postgres://local"), expectedDBEnvName, " ")},
		{name: "invalid opt in", values: withValue(cloneValues(base), enabledEnvName, "yes")},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := loadConfig(mapLookup(test.values))
			if !errors.Is(err, errInvalidConfiguration) {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestLoadConfigUsesDefaultManifestPath(t *testing.T) {
	values := map[string]string{
		enabledEnvName:    "true",
		dsnEnvName:        "postgres://local",
		expectedDBEnvName: "seed_tests",
	}
	loaded, enabled, err := loadConfig(mapLookup(values))
	if err != nil || !enabled || loaded.manifestPath != defaultManifestPath {
		t.Fatal("default manifest path was not selected")
	}
}

func TestRunIsNoOpWhenDisabled(t *testing.T) {
	count, err := run(context.Background(), mapLookup(map[string]string{}))
	if err != nil {
		t.Fatal("disabled run required a database")
	}
	if count != 0 {
		t.Fatal("disabled run reported seeded accounts")
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}
}

func cloneValues(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func withValue(values map[string]string, key, value string) map[string]string {
	result := cloneValues(values)
	result[key] = value
	return result
}
