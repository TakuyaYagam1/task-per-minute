package task

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

var errInvalidSourceFileURL = errors.New("invalid task source file URL")

func sourceFileUploadKey(taskID, uploadID uuid.UUID) string {
	return fmt.Sprintf("tasks/%s/sources/%s.zip", taskID, uploadID)
}

func sourceFileKeyFromURL(taskID uuid.UUID, rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !validSourceFileLocation(parsed) {
		return "", errInvalidSourceFileURL
	}

	segments, ok := sourceFileObjectSegments(taskID, parsed.EscapedPath())
	if !ok || !validSourceFileName(segments[3]) {
		return "", errInvalidSourceFileURL
	}
	return strings.Join(segments, "/"), nil
}

func validSourceFileLocation(parsed *url.URL) bool {
	if parsed == nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func sourceFileObjectSegments(taskID uuid.UUID, escapedPath string) ([]string, bool) {
	segments := strings.Split(strings.Trim(escapedPath, "/"), "/")
	if len(segments) < 4 {
		return nil, false
	}
	segments = segments[len(segments)-4:]
	if segments[0] != "tasks" || segments[1] != taskID.String() || segments[2] != "sources" {
		return nil, false
	}
	return segments, true
}

func validSourceFileName(fileName string) bool {
	const zipSuffix = ".zip"
	if !strings.HasSuffix(fileName, zipSuffix) {
		return false
	}
	uploadIDText := strings.TrimSuffix(fileName, zipSuffix)
	uploadID, err := uuid.Parse(uploadIDText)
	return err == nil && uploadID != uuid.Nil && uploadID.String() == uploadIDText
}
