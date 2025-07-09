package config

import (
	"encoding/json"
	"fmt"
	"time"
)

// Duration wraps time.Duration to provide JSON marshaling/unmarshaling
type Duration struct {
	time.Duration
}

// UnmarshalJSON implements json.Unmarshaler interface
func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}

	switch value := v.(type) {
	case float64:
		// Handle numeric values (nanoseconds)
		d.Duration = time.Duration(value)
		return nil
	case string:
		// Handle string values like "10s", "5m", "2h"
		var err error
		d.Duration, err = time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid duration format '%s': %w", value, err)
		}
		return nil
	default:
		return fmt.Errorf("invalid duration value: %v", value)
	}
}

// MarshalJSON implements json.Marshaler interface
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

// String returns the string representation
func (d Duration) String() string {
	return d.Duration.String()
}
