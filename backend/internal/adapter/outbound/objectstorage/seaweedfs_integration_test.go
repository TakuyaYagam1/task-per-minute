//go:build integration

package objectstorage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/moby/moby/api/types/container"
	containernetwork "github.com/moby/moby/api/types/network"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	avatarIntegrationAccessKey = "synthetic-avatar-access"
	avatarIntegrationSecretKey = "synthetic-avatar-secret"
)

func TestSeaweedStorageAvatarPrivateRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	identities, err := json.Marshal(map[string]any{
		"identities": []any{
			map[string]any{
				"name": "avatar-integration",
				"credentials": []any{
					map[string]string{
						"accessKey": avatarIntegrationAccessKey,
						"secretKey": avatarIntegrationSecretKey,
					},
				},
				"actions": []string{"Admin"},
			},
		},
	})
	require.NoError(t, err)

	containerInstance, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:      "chrislusf/seaweedfs:4.20",
			Entrypoint: []string{"weed"},
			Cmd: []string{
				"server", "-dir=/data", "-master.port=9333", "-volume.port=8090",
				"-filer", "-filer.port=8888", "-s3", "-s3.port=8333",
				"-s3.config=/tmp/seaweedfs-s3.json",
			},
			Files: []testcontainers.ContainerFile{{
				Reader:            bytes.NewReader(identities),
				ContainerFilePath: "/tmp/seaweedfs-s3.json",
				FileMode:          0o600,
			}},
			ExposedPorts: []string{"8333/tcp"},
			HostConfigModifier: func(config *container.HostConfig) {
				config.PortBindings = containernetwork.PortMap{
					containernetwork.MustParsePort("8333/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1")}},
				}
			},
			WaitingFor: wait.ForListeningPort("8333/tcp").WithStartupTimeout(75 * time.Second),
		},
		Started: true,
	})
	if containerInstance != nil {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cleanupCancel()
			_ = containerInstance.Terminate(cleanupCtx)
		})
	}
	require.NoError(t, err)

	host, err := containerInstance.Host(ctx)
	require.NoError(t, err)
	port, err := containerInstance.MappedPort(ctx, "8333/tcp")
	require.NoError(t, err)
	endpoint := net.JoinHostPort(host, port.Port())
	storage, err := New(Config{
		Endpoint: endpoint, AccessKey: avatarIntegrationAccessKey, SecretKey: avatarIntegrationSecretKey,
		Bucket: "avatar-integration", Secure: false,
	})
	require.NoError(t, err)

	deadline := time.Now().Add(15 * time.Second)
	for {
		ensureCtx, ensureCancel := context.WithTimeout(ctx, 2*time.Second)
		err = storage.EnsureBucket(ensureCtx)
		ensureCancel()
		if err == nil || time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, err)

	playerID, objectID := uuid.New(), uuid.New()
	key := fmt.Sprintf("avatars/%s/%s.png", playerID, objectID)
	var source bytes.Buffer
	imageData := image.NewNRGBA(image.Rect(0, 0, 12, 9))
	imageData.SetNRGBA(3, 4, color.NRGBA{R: 25, G: 80, B: 180, A: 210})
	require.NoError(t, png.Encode(&source, imageData))
	require.NoError(t, storage.PutAvatar(ctx, key, source.Bytes(), "image/png"))

	object, err := storage.client.GetObject(ctx, storage.bucket, key, minio.GetObjectOptions{})
	require.NoError(t, err)
	info, err := object.Stat()
	require.NoError(t, err)
	require.Equal(t, "image/png", info.ContentType)
	require.NoError(t, object.Close())

	unsignedRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://"+endpoint+"/avatar-integration/"+key,
		nil,
	)
	require.NoError(t, err)
	unsignedResponse, err := http.DefaultClient.Do(unsignedRequest)
	require.NoError(t, err)
	defer unsignedResponse.Body.Close()
	require.Contains(
		t,
		[]int{http.StatusUnauthorized, http.StatusForbidden},
		unsignedResponse.StatusCode,
		"avatar objects must reject reads without S3 credentials",
	)

	got, err := storage.GetAvatar(ctx, key, maxAvatarObjectBytes)
	require.NoError(t, err)
	require.Equal(t, source.Bytes(), got)
	require.NoError(t, storage.DeleteAvatar(ctx, key))
	require.NoError(t, storage.DeleteAvatar(ctx, key), "deletion should be idempotent")
	_, err = storage.GetAvatar(ctx, key, maxAvatarObjectBytes)
	require.Error(t, err)

	videoData, err := os.ReadFile(filepath.Join("..", "media", "ffmpeg", "testdata", "avatar.mp4"))
	require.NoError(t, err)
	videoKey := fmt.Sprintf("avatars/%s/%s.mp4", playerID, uuid.New())
	require.NoError(t, storage.PutAvatar(ctx, videoKey, videoData, "video/mp4"))
	videoObject, err := storage.client.GetObject(ctx, storage.bucket, videoKey, minio.GetObjectOptions{})
	require.NoError(t, err)
	videoInfo, err := videoObject.Stat()
	require.NoError(t, err)
	require.Equal(t, "video/mp4", videoInfo.ContentType)
	require.NoError(t, videoObject.Close())
	storedVideo, err := storage.GetAvatar(ctx, videoKey, maxAvatarObjectBytes)
	require.NoError(t, err)
	require.Equal(t, videoData, storedVideo)
	require.NoError(t, storage.DeleteAvatar(ctx, videoKey))
}
