//go:build integration && capacity

package integration_test

import (
	"context"
	"net/netip"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/distribution/reference"
	dockerclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

func tournamentCapacityRunningPostgresImage(tb testing.TB) tournamentCapacityImageDigest {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), tournamentCapacityOperationTimeout)
	defer cancel()
	require.NotNil(tb, sharedPool)
	require.NoError(tb, sharedPool.Ping(ctx))
	poolConfig := sharedPool.Config().ConnConfig
	require.NotEmpty(tb, strings.TrimSpace(poolConfig.Host))
	require.NotZero(tb, poolConfig.Port)

	provider, err := testcontainers.NewDockerProvider()
	require.NoError(tb, err)
	tb.Cleanup(func() { require.NoError(tb, provider.Close()) })
	containers, err := provider.Client().ContainerList(ctx, dockerclient.ContainerListOptions{})
	require.NoError(tb, err)

	matchingIDs := make([]string, 0, 1)
	for _, candidate := range containers.Items {
		if string(candidate.State) != "running" {
			continue
		}
		for _, published := range candidate.Ports {
			if published.PrivatePort == 5432 &&
				published.PublicPort == poolConfig.Port &&
				published.Type == "tcp" &&
				tournamentCapacityMappedHostMatches(poolConfig.Host, published.IP) {
				matchingIDs = append(matchingIDs, candidate.ID)
				break
			}
		}
	}
	require.Lenf(tb, matchingIDs, 1,
		"expected exactly one running container mapped to sharedPool endpoint %s:%d",
		poolConfig.Host, poolConfig.Port)

	inspection, err := provider.Client().ContainerInspect(
		ctx,
		matchingIDs[0],
		dockerclient.ContainerInspectOptions{},
	)
	require.NoError(tb, err)
	container := inspection.Container
	require.Equal(tb, matchingIDs[0], container.ID)
	require.NotNil(tb, container.State)
	require.True(tb, container.State.Running)
	require.Equal(tb, "running", string(container.State.Status))
	require.NotNil(tb, container.Config)
	require.Equal(tb,
		tournamentCapacityNormalizedImageReference(tb, tournamentCapacityPostgresImage),
		tournamentCapacityNormalizedImageReference(tb, container.Config.Image),
		"running database container was not created from the requested capacity image reference")
	require.True(tb, strings.HasPrefix(container.Image, "sha256:"))
	require.Len(tb, container.Image, len("sha256:")+64)
	require.True(tb, tournamentCapacityHex(strings.TrimPrefix(container.Image, "sha256:")))

	return tournamentCapacityImageDigest{
		Reference:   tournamentCapacityPostgresImage,
		Digest:      container.Image,
		DigestScope: "content",
	}
}

func tournamentCapacityNormalizedImageReference(tb testing.TB, value string) string {
	tb.Helper()
	named, err := reference.ParseNormalizedNamed(value)
	require.NoError(tb, err)
	return reference.TagNameOnly(named).String()
}

func tournamentCapacityMappedHostMatches(poolHost string, publishedHost netip.Addr) bool {
	if !publishedHost.IsValid() || publishedHost.IsUnspecified() {
		return true
	}

	poolHost = strings.TrimSpace(poolHost)
	poolHost = strings.TrimPrefix(poolHost, "[")
	poolHost = strings.TrimSuffix(poolHost, "]")
	if zoneIndex := strings.LastIndexByte(poolHost, '%'); zoneIndex >= 0 {
		poolHost = poolHost[:zoneIndex]
	}
	poolAddress, err := netip.ParseAddr(poolHost)
	if err == nil {
		return poolAddress.Unmap() == publishedHost.Unmap()
	}
	return strings.EqualFold(poolHost, "localhost") && publishedHost.IsLoopback()
}

func tournamentCapacityHex(value string) bool {
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func tournamentCapacityTestdataPath(tb testing.TB, name string) string {
	tb.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(tb, ok)
	return filepath.Join(filepath.Dir(sourceFile), "testdata", "capacity", name)
}
