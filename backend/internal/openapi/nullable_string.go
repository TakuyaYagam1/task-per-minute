package openapi

import (
	"bytes"
	"encoding/json"
)

// NullableString preserves the three JSON states needed by PATCH-like request
// fields: absent, explicit null, and a concrete string value.
type NullableString map[bool]string

func NewNullableString(value string) NullableString {
	return NullableString{true: value}
}

func NullString() NullableString {
	return NullableString{false: ""}
}

func (s NullableString) MarshalJSON() ([]byte, error) {
	if value, ok := s[true]; ok {
		return json.Marshal(value)
	}
	return []byte("null"), nil
}

func (s *NullableString) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*s = NullString()
		return nil
	}

	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = NewNullableString(value)
	return nil
}

func (s NullableString) IsSet() bool {
	return s != nil
}

func (s NullableString) Value() (*string, bool) {
	if value, ok := s[true]; ok {
		return &value, true
	}
	return nil, s != nil
}
