package interpreter

import (
	"strings"
	"testing"
)

func TestStringFalse_UnsupportedType(t *testing.T) {
	var sf StringFalse
	err := sf.UnmarshalJSON([]byte("[1,2,3]"))

	if err == nil {
		t.Fatal("expected error for unsupported type")
	}

	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("expected 'unsupported type' error, got %v", err)
	}
}
