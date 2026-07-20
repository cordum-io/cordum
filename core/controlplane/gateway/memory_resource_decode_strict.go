package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// UnmarshalJSON rejects duplicate and unknown top-level fields rather than
// accepting encoding/json's last-value-wins behavior for security authority.
func (request *memoryResolveRequest) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return errors.New("memory resolve request must be an object")
	}
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("memory resolve field name required")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("duplicate memory resolve field")
		}
		seen[name] = struct{}{}
		switch name {
		case "job_id":
			err = decoder.Decode(&request.JobID)
		case "reference":
			err = decoder.Decode(&request.Reference)
		default:
			return errors.New("unknown memory resolve field")
		}
		if err != nil {
			return err
		}
	}
	if _, err = decoder.Token(); err != nil {
		return err
	}
	if token, err = decoder.Token(); !errors.Is(err, io.EOF) || token != nil {
		return errors.New("invalid memory resolve object")
	}
	return nil
}
