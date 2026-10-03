package control

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNameRunes bounds a bridge label so it fits list rows and headers.
const MaxNameRunes = 64

// NormalizeName trims a bridge label and rejects control characters, invalid
// UTF-8 and labels longer than MaxNameRunes. "" means "no name".
func NormalizeName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", errors.New("el nombre no es UTF-8 válido")
	}
	name = strings.TrimSpace(name)
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errors.New("el nombre no admite caracteres de control")
	}
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return "", errors.New("el nombre admite como máximo 64 caracteres")
	}
	return name, nil
}

// handleName implements POST /v1/name {"name": "..."}; an empty name resets
// the label so readers fall back to the project directory.
func (e *Endpoint) handleName(w http.ResponseWriter, r *http.Request) {
	requestID, ok := e.authorize(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, requestID, "INVALID_METHOD", "name requires POST", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	var input struct {
		Name *string `json:"name"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4097))
	err := decoder.Decode(&input)
	var extra any
	if err != nil || decoder.Decode(&extra) != io.EOF || input.Name == nil {
		writeError(w, requestID, "INVALID_JSON", "name requires {\"name\": string}", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	name, err := NormalizeName(*input.Name)
	if err != nil {
		writeError(w, requestID, "INVALID_NAME", err.Error(), false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	e.roles.SetName(name)
	_ = writeJSON(w, http.StatusOK, nameResponse{V: 1, Type: "response", RequestID: requestID, OK: true, Operation: "name", InstanceID: e.descriptor.InstanceID, Name: name})
}

// nameResponse keeps "name" even when empty: a reset is an answer.
type nameResponse struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	RequestID  string `json:"request_id,omitempty"`
	OK         bool   `json:"ok"`
	Operation  string `json:"operation"`
	InstanceID string `json:"instance_id"`
	Name       string `json:"name"`
}
