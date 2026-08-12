package v1

import (
	"errors"
	"mime/multipart"
	"net/http"

	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

const multipartMemory = 32 << 20

var errUploadTooLarge = errors.New("upload too large")

func parseSourceFile(w http.ResponseWriter, r *http.Request) (multipart.File, *multipart.FileHeader, error) {
	r.Body = http.MaxBytesReader(w, r.Body, adminusecase.MaxSourceFileSize+(1<<20))
	//nolint:gosec,nolintlint // G120 in newer gosec: MaxBytesReader bounds multipart parsing before form parsing.
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil, nil, errUploadTooLarge
		}
		return nil, nil, err
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, nil, err
	}
	if header.Size > adminusecase.MaxSourceFileSize {
		_ = file.Close()
		return nil, nil, errUploadTooLarge
	}
	return file, header, nil
}
