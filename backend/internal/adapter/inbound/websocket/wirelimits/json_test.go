package wirelimits

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateJSONRejectsUnsafeStructures(t *testing.T) {
	t.Parallel()

	deep := strings.Repeat("[", MaxJSONDepth) + "0" + strings.Repeat("]", MaxJSONDepth)
	tooMany := "[" + strings.Repeat("0,", MaxCollectionItems) + "0]"
	boundedString := `"` + strings.Repeat("a", MaxStringBytes) + `"`
	tooLarge := "[" + strings.Join([]string{boundedString, boundedString, boundedString, boundedString}, ",") + "]"
	tests := []struct {
		name string
		body string
	}{
		{name: "duplicate root key", body: `{"type":"public","type":"operator"}`},
		{name: "case folded duplicate key", body: `{"type":"public","TYPE":"operator"}`},
		{name: "unicode folded duplicate key", body: `{"state":"first","\u017ftate":"second"}`},
		{name: "nested duplicate key", body: `{"payload":{"scope":"first","scope":"second"}}`},
		{name: "too deep", body: deep},
		{name: "string too long", body: `{"value":"` + strings.Repeat("a", MaxStringBytes+1) + `"}`},
		{name: "collection too large", body: tooMany},
		{name: "message too large", body: tooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateJSON([]byte(tt.body)); !errors.Is(err, ErrInvalidJSON) {
				t.Fatalf("ValidateJSON() error = %v, want %v", err, ErrInvalidJSON)
			}
		})
	}
}

func TestValidateJSONAcceptsBoundaryValues(t *testing.T) {
	t.Parallel()
	items := "[" + strings.Repeat("0,", MaxCollectionItems-1) + "0]"
	body := `{"label":"` + strings.Repeat("a", MaxStringBytes) + `","items":` + items + `}`
	if err := ValidateJSON([]byte(body)); err != nil {
		t.Fatalf("ValidateJSON() error = %v", err)
	}
	allowedDepth := strings.Repeat("[", MaxJSONDepth-1) + "0" + strings.Repeat("]", MaxJSONDepth-1)
	if err := ValidateJSON([]byte(allowedDepth)); err != nil {
		t.Fatalf("ValidateJSON() rejected maximum depth: %v", err)
	}
}
