package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"
)

func lowerHex(s string, size int) bool {
	if len(s) != size {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func requestID(r *http.Request) (string, *failure) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !lowerHex(values[0], 32) {
		return "", fail(400, "INVALID_REQUEST", "A 32-character lowercase hexadecimal Idempotency-Key is required.")
	}
	return values[0], nil
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, *failure) {
	if r.Body == nil {
		return nil, nil
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return nil, fail(413, "PAYLOAD_TOO_LARGE", "The request body is too large.")
		}
		return nil, fail(400, "INVALID_REQUEST", "The request body could not be read.")
	}
	return data, nil
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) *failure {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return fail(415, "UNSUPPORTED_MEDIA_TYPE", "Use application/json.")
	}
	data, problem := readBody(w, r)
	if problem != nil {
		return problem
	}
	return decodeJSON(data, target)
}

func decodeJSON(data []byte, target any) *failure {
	if !utf8.Valid(data) || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return invalidJSON()
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := uniqueJSON(dec); err != nil {
		return invalidJSON()
	}
	if _, err := dec.Token(); err != io.EOF {
		return invalidJSON()
	}
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return invalidJSON()
	}
	return nil
}

// encoding/json accepts repeated keys by default; the protocol requires a
// single interpretation, including escaped keys and nested objects.
func uniqueJSON(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate or invalid JSON key")
			}
			seen[name] = true
			if err := uniqueJSON(dec); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := uniqueJSON(dec); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = dec.Token()
	return err
}

func invalidJSON() *failure {
	return fail(400, "INVALID_REQUEST", "Use one JSON object with the required fields and no duplicate or unknown keys.")
}
