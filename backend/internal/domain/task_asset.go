package domain

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

func TaskSourceFileKey(taskID uuid.UUID) string {
	return fmt.Sprintf("tasks/%s/source.zip", taskID)
}

func TaskSourceFileUploadKey(taskID, uploadID uuid.UUID) string {
	return fmt.Sprintf("tasks/%s/sources/%s.zip", taskID, uploadID)
}

func TaskSourceFileKeyFromURL(taskID uuid.UUID, rawURL string) string {
	needle := "tasks/" + taskID.String() + "/"
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return TaskSourceFileKey(taskID)
	}
	path := strings.TrimPrefix(parsed.Path, "/")
	if idx := strings.Index(path, needle); idx >= 0 {
		return path[idx:]
	}
	return TaskSourceFileKey(taskID)
}
