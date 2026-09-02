package integration_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArenaMetricsExposureSource(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile(arenaCaddyfilePath())
	require.NoError(t, err)

	sites := arenaCaddySiteBlocks(string(contents))
	require.NotEmpty(t, sites)
	for name, body := range sites {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			exactDeny := strings.Index(body, "handle /internal {")
			prefixDeny := strings.Index(body, "handle /internal/* {")
			firstProxy := strings.Index(body, "reverse_proxy ")
			require.NotEqual(t, -1, exactDeny, "public site must deny /internal")
			require.NotEqual(t, -1, prefixDeny, "public site must deny /internal/*")
			require.NotEqual(t, -1, firstProxy, "public site must keep an upstream")
			require.Less(t, exactDeny, firstProxy)
			require.Less(t, prefixDeny, firstProxy)
			require.Contains(t, body[exactDeny:firstProxy], "respond 404")
			require.Contains(t, body[prefixDeny:firstProxy], "respond 404")
			require.NotContains(t, body, "handle /internal/metrics")
		})
	}
}

func arenaCaddyfilePath() string {
	_, sourceFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(sourceFile), "..", "..", "deployment", "caddy", "Caddyfile")
}

func arenaCaddySiteBlocks(source string) map[string]string {
	blocks := make(map[string]string)
	lines := strings.Split(source, "\n")
	depth := 0
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if depth != 0 {
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			continue
		}
		if !strings.HasSuffix(line, " {") || strings.HasPrefix(line, "(") {
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			continue
		}

		name := strings.TrimSuffix(line, " {")
		blockDepth := strings.Count(line, "{") - strings.Count(line, "}")
		bodyStart := index + 1
		for index++; index < len(lines) && blockDepth > 0; index++ {
			blockDepth += strings.Count(lines[index], "{") - strings.Count(lines[index], "}")
		}
		blocks[name] = strings.Join(lines[bodyStart:index-1], "\n")
		index--
	}
	return blocks
}
