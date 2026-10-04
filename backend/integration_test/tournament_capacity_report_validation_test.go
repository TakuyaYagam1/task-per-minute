//go:build integration && capacity

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

func validateTournamentCapacityJSON(tb testing.TB, data []byte) {
	tb.Helper()
	schema := loadTournamentCapacitySchema(tb, tournamentCapacityTestdataPath(tb, tournamentCapacitySchemaName))
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	require.NoError(tb, err)
	require.NoError(tb, schema.Validate(document))
}

func loadTournamentCapacitySchema(tb testing.TB, path string) *jsonschema.Schema {
	tb.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	schema, err := compiler.Compile(path)
	require.NoError(tb, err)
	return schema
}

func tournamentCapacityPercentiles(samples []time.Duration) (float64, float64, float64, float64) {
	if len(samples) == 0 {
		return 0, 0, 0, 0
	}
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	value := func(percentile float64) float64 {
		index := int(float64(len(ordered))*percentile+0.999999999) - 1
		if index < 0 {
			index = 0
		}
		if index >= len(ordered) {
			index = len(ordered) - 1
		}
		return float64(ordered[index]) / float64(time.Millisecond)
	}
	return value(0.50), value(0.95), value(0.99), value(1)
}

func tournamentCapacityLimit(metric, comparator string, limit, observed float64, unit string) tournamentCapacityThreshold {
	return tournamentCapacityThreshold{
		Metric:     metric,
		Comparator: comparator,
		Limit:      limit,
		Observed:   observed,
		Unit:       unit,
		Status:     tournamentCapacityThresholdStatus(comparator, limit, observed),
	}
}

func tournamentCapacityThresholdStatus(comparator string, limit, observed float64) tournamentCapacityStatus {
	switch comparator {
	case "<=":
		return tournamentCapacityStatusFor(observed <= limit)
	case ">=":
		return tournamentCapacityStatusFor(observed >= limit)
	default:
		return tournamentCapacityFail
	}
}

func tournamentCapacityStatusFor(ok bool) tournamentCapacityStatus {
	if ok {
		return tournamentCapacityPass
	}
	return tournamentCapacityFail
}

func tournamentCapacityBuildIdentity(tb testing.TB) tournamentCapacityIdentity {
	tb.Helper()
	commit := strings.ToLower(tournamentCapacityGitOutput(tb, "rev-parse", "--verify", "HEAD^{commit}"))
	require.Len(tb, commit, 40)
	require.True(tb, tournamentCapacityHex(commit))

	dirty := tournamentCapacityGitOutput(tb, "status", "--porcelain=v1", "--untracked-files=all", "--") != ""
	goVersion := runtime.Version()
	target := runtime.GOOS + "/" + runtime.GOARCH
	buildDigest := tournamentCapacityDigest([]byte(strings.Join([]string{
		commit,
		strconv.FormatBool(dirty),
		goVersion,
		target,
	}, "\x00")))
	return tournamentCapacityIdentity{
		Revision: tournamentCapacityRevision{Commit: commit, Dirty: dirty},
		Build: tournamentCapacityBuild{
			GoVersion: goVersion,
			Target:    target,
			Digest:    buildDigest,
		},
	}
}

type tournamentCapacityBoundedOutput struct {
	data     []byte
	exceeded bool
}

func (output *tournamentCapacityBoundedOutput) Write(data []byte) (int, error) {
	written := len(data)
	remaining := tournamentCapacityGitOutputLimit - len(output.data)
	if remaining <= 0 {
		output.exceeded = true
		return written, nil
	}
	if len(data) > remaining {
		output.exceeded = true
		data = data[:remaining]
	}
	output.data = append(output.data, data...)
	return written, nil
}

func (output *tournamentCapacityBoundedOutput) String() string {
	return string(output.data)
}

func tournamentCapacityGitOutput(tb testing.TB, args ...string) string {
	tb.Helper()
	gitPath, err := exec.LookPath(tournamentCapacityGitExecutable)
	require.NoError(tb, err)
	gitPath, err = filepath.EvalSymlinks(gitPath)
	require.NoError(tb, err)
	gitInfo, err := os.Stat(gitPath)
	require.NoError(tb, err)
	require.True(tb, gitInfo.Mode().IsRegular())
	require.Zero(tb, gitInfo.Mode().Perm()&0o022)

	ctx, cancel := context.WithTimeout(context.Background(), tournamentCapacityGitCommandTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, gitPath, args...)
	command.Dir = tournamentCapacityRepositoryRoot(tb)
	command.Env = []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"HOME=/nonexistent",
		"LANG=C",
		"LC_ALL=C",
		"PATH=/nonexistent",
	}
	var stdout tournamentCapacityBoundedOutput
	var stderr tournamentCapacityBoundedOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if ctx.Err() != nil {
		require.NoError(tb, ctx.Err(), "bounded git command timed out")
	}
	require.False(tb, stdout.exceeded, "git stdout exceeded %d bytes", tournamentCapacityGitOutputLimit)
	require.False(tb, stderr.exceeded, "git stderr exceeded %d bytes", tournamentCapacityGitOutputLimit)
	require.NoErrorf(tb, err, "git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	return strings.TrimSpace(stdout.String())
}

func tournamentCapacityRepositoryRoot(tb testing.TB) string {
	tb.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(tb, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
}

func tournamentCapacityDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", digest)
}
