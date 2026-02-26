package interpreter

import (
	"encoding/json"
	"fmt"
)

type StringFalse struct {
	Value string
}

func (sf *StringFalse) UnmarshalJSON(data []byte) error {
	// Try to parse bool
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		if b {
			sf.Value = "true"
		} else {
			sf.Value = ""
		}
		return nil
	}

	// Try to parse string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		sf.Value = s
		return nil
	}

	return fmt.Errorf("unsupported type: %s", string(data))
}
