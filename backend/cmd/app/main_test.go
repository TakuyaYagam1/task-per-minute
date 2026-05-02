package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
)

func TestRunReturnsZeroOnSuccess(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	code := run(runDeps{
		loadConfig: func() (*config.Config, error) {
			return cfg, nil
		},
		newLogger: func() (logkit.Logger, error) {
			return logkit.Noop(), nil
		},
		runApp: func(got *config.Config, log logkit.Logger) error {
			require.Same(t, cfg, got)
			require.NotNil(t, log)
			return nil
		},
	})

	require.Equal(t, 0, code)
}

func TestRunReturnsNonZeroWhenAppRunFails(t *testing.T) {
	t.Parallel()

	appErr := errors.New("listen failed")
	code := run(runDeps{
		loadConfig: func() (*config.Config, error) {
			return &config.Config{}, nil
		},
		newLogger: func() (logkit.Logger, error) {
			return logkit.Noop(), nil
		},
		runApp: func(*config.Config, logkit.Logger) error {
			return appErr
		},
	})

	require.Equal(t, 1, code)
}

func TestRunReturnsNonZeroWhenConfigLoadFails(t *testing.T) {
	t.Parallel()

	code := run(runDeps{
		loadConfig: func() (*config.Config, error) {
			return nil, errors.New("missing env")
		},
		newLogger: func() (logkit.Logger, error) {
			t.Fatal("logger must not be created when config load fails")
			return nil, nil
		},
		runApp: func(*config.Config, logkit.Logger) error {
			t.Fatal("app must not start when config load fails")
			return nil
		},
	})

	require.Equal(t, 1, code)
}
