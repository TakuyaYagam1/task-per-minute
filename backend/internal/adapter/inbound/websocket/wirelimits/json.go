package wirelimits

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	MaxMessageBytes    = 16 * 1024
	MaxJSONDepth       = 32
	MaxStringBytes     = 4 * 1024
	MaxCollectionItems = 128
)

var ErrInvalidJSON = errors.New("invalid realtime JSON")

// ValidateJSON applies transport limits and rejects duplicate keys before
// encoding/json can overwrite an earlier value during semantic decoding.
func ValidateJSON(data []byte) error {
	if len(data) == 0 || len(data) > MaxMessageBytes || !utf8.Valid(data) {
		return ErrInvalidJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateValue(decoder, 1); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalidJSON
	}
	return nil
}

func StringWithinLimit(value string) bool {
	return utf8.ValidString(value) && len(value) <= MaxStringBytes
}

func CollectionWithinLimit(length int) bool {
	return length >= 0 && length <= MaxCollectionItems
}

func validateValue(decoder *json.Decoder, depth int) error {
	if depth > MaxJSONDepth {
		return errors.New("maximum nesting depth exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			return validateObject(decoder, depth)
		case '[':
			return validateArray(decoder, depth)
		default:
			return errors.New("unexpected closing delimiter")
		}
	case string:
		if !StringWithinLimit(value) {
			return errors.New("maximum string length exceeded")
		}
	case nil, bool, json.Number:
		return nil
	default:
		return errors.New("unsupported JSON token")
	}
	return nil
}

func validateObject(decoder *json.Decoder, depth int) error {
	seen := make([]string, 0, MaxCollectionItems)
	fields := 0
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok || !StringWithinLimit(key) {
			return errors.New("invalid object key")
		}
		for _, existing := range seen {
			if strings.EqualFold(existing, key) {
				return fmt.Errorf("duplicate object key %q", key)
			}
		}
		seen = append(seen, key)
		fields++
		if !CollectionWithinLimit(fields) {
			return errors.New("maximum object fields exceeded")
		}
		if err := validateValue(decoder, depth+1); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return errors.New("unterminated object")
	}
	return nil
}

func validateArray(decoder *json.Decoder, depth int) error {
	items := 0
	for decoder.More() {
		items++
		if !CollectionWithinLimit(items) {
			return errors.New("maximum array items exceeded")
		}
		if err := validateValue(decoder, depth+1); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim(']') {
		return errors.New("unterminated array")
	}
	return nil
}
