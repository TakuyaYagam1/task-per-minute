package postgres

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMarshalJSONPreservesEmptyArrayForNilSlice(t *testing.T) {
	var values []string

	data, err := marshalJSON("test nil slice", values)
	if err != nil {
		t.Fatalf("marshal nil slice: %v", err)
	}
	if string(data) != "[]" {
		t.Fatalf("marshal nil slice = %s, want []", data)
	}
}

func TestMarshalJSONReturnsContextualError(t *testing.T) {
	_, err := marshalJSON("test unsupported value", make(chan struct{}))
	if err == nil {
		t.Fatal("marshal unsupported value: expected error")
	}
	if !strings.Contains(err.Error(), "test unsupported value - marshal JSON") {
		t.Fatalf("marshal unsupported value error = %q", err)
	}
	var unsupported *json.UnsupportedTypeError
	if !errors.As(err, &unsupported) {
		t.Fatalf("marshal unsupported value error type = %T", err)
	}
}
