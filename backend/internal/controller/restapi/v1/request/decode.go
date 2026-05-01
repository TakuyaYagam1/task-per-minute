package request

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const maxBodyBytes = 1 << 20

var (
	ErrBodyTooLarge = errors.New("request body is too large")
	ErrEmptyBody    = errors.New("request body is empty")
	ErrTrailingData = errors.New("request body must contain a single json value")
)

// UnknownFieldsErr reports a JSON field that is not present in the target DTO.
type UnknownFieldsErr struct {
	Field string
}

func (e *UnknownFieldsErr) Error() string {
	if e == nil || e.Field == "" {
		return "request body contains unknown field"
	}
	return fmt.Sprintf("request body contains unknown field %q", e.Field)
}

// DecodeJSON decodes a JSON request body into v with a strict 1 MB limit.
func DecodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return ErrEmptyBody
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(body) > maxBodyBytes {
		return ErrBodyTooLarge
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return ErrEmptyBody
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(v); err != nil {
		return normalizeDecodeError(err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return ErrTrailingData
	}
	return nil
}

func normalizeDecodeError(err error) error {
	const unknownFieldPrefix = "json: unknown field "

	if strings.HasPrefix(err.Error(), unknownFieldPrefix) {
		rawField := strings.TrimPrefix(err.Error(), unknownFieldPrefix)
		field, unquoteErr := strconv.Unquote(rawField)
		if unquoteErr != nil {
			field = strings.Trim(rawField, `"`)
		}
		return &UnknownFieldsErr{Field: field}
	}
	return err
}
