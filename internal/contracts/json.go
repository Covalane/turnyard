package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/Covalane/turnyard/internal/fault"
)

// parseUnique rejects duplicate keys at every nesting level, unlike encoding/json.Unmarshal.
func parseUnique(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		obj := map[string]any{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key := keyTok.(string)
			if _, exists := obj[key]; exists {
				return nil, fault.New(fault.CodeInvalidJson, "duplicate JSON key: %s", key)
			}
			v, err := parseUnique(dec)
			if err != nil {
				return nil, err
			}
			obj[key] = v
		}
		_, err := dec.Token()
		return obj, err
	case '[':
		arr := []any{}
		for dec.More() {
			v, err := parseUnique(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token()
		return arr, err
	default:
		return nil, fault.New(fault.CodeInvalidJson, "unexpected JSON delimiter")
	}
}

func ReadJSON(path string, kind string, dest any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fault.Wrap(fault.CodeInvalidJson, "", err, "read %s", path)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := parseUnique(dec)
	if err != nil {
		var coded *fault.Error
		if errors.As(err, &coded) {
			return err
		}
		return fault.Wrap(fault.CodeInvalidJson, "", err, "%s", path)
	}
	if _, ok := value.(map[string]any); !ok {
		return fault.New(fault.CodeInvalidJson, "%s must contain an object", path)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fault.New(fault.CodeInvalidJson, "%s has trailing JSON", path)
	}
	if kind != "" {
		schema, err := schemaFor(kind)
		if err != nil {
			return err
		}
		if err := schema.Validate(value); err != nil {
			return fault.Wrap(fault.CodeInvalidSpec, "", err, "%s", kind)
		}
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(normalized, dest); err != nil {
		return fault.Wrap(fault.CodeInvalidJson, "", err, "%s", path)
	}
	return nil
}
