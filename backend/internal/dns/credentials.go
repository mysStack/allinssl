package dns

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var ErrCredential = errors.New("DNS_CREDENTIAL_UNAVAILABLE")

type CredentialSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Credential is execution-only; do not put it in a response or audit DTO.
type Credential struct{ accessKeyID, accessKeySecret string }
type SQLCredentialStore struct{ DB *sql.DB }

func (Credential) String() string { return "Credential{redacted}" }

func (Credential) GoString() string { return "dns.Credential{redacted}" }

func parseCredential(config string) (Credential, error) {
	if len(config) == 0 || len(config) > 65536 {
		return Credential{}, ErrCredential
	}

	decoder := json.NewDecoder(strings.NewReader(config))
	decoder.DisallowUnknownFields()
	token, err := decoder.Token()
	if err != nil {
		return Credential{}, ErrCredential
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return Credential{}, ErrCredential
	}

	values := make(map[string]string, 2)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return Credential{}, ErrCredential
		}
		key, ok := token.(string)
		if !ok || (key != "access_key_id" && key != "access_key_secret") {
			return Credential{}, ErrCredential
		}
		if _, exists := values[key]; exists {
			return Credential{}, ErrCredential
		}

		var value string
		if err := decoder.Decode(&value); err != nil {
			return Credential{}, ErrCredential
		}
		values[key] = value
	}

	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return Credential{}, ErrCredential
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Credential{}, ErrCredential
	}

	credential := Credential{
		accessKeyID:     values["access_key_id"],
		accessKeySecret: values["access_key_secret"],
	}
	if !validCredentialValue(credential.accessKeyID) || !validCredentialValue(credential.accessKeySecret) {
		return Credential{}, ErrCredential
	}
	return credential, nil
}

func (s SQLCredentialStore) List(ctx context.Context) ([]CredentialSummary, error) {
	if s.DB == nil {
		return nil, ErrCredential
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, type FROM access WHERE type = ? ORDER BY id ASC`, "aliyun")
	if err != nil {
		return nil, credentialStoreError(ctx)
	}
	defer rows.Close()

	credentials := make([]CredentialSummary, 0)
	for rows.Next() {
		var credential CredentialSummary
		if err := rows.Scan(&credential.ID, &credential.Name, &credential.Type); err != nil {
			return nil, credentialStoreError(ctx)
		}
		if credential.ID <= 0 || credential.Name == "" || credential.Type != "aliyun" {
			return nil, ErrCredential
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, credentialStoreError(ctx)
	}
	return credentials, nil
}

func (s SQLCredentialStore) Resolve(ctx context.Context, id int64) (Credential, error) {
	if s.DB == nil || id <= 0 {
		return Credential{}, ErrCredential
	}

	var config string
	err := s.DB.QueryRowContext(ctx, `SELECT config FROM access WHERE id = ? AND type = ?`, id, "aliyun").Scan(&config)
	if err != nil {
		return Credential{}, credentialStoreError(ctx)
	}
	return parseCredential(config)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected credential JSON")
	}
	return nil
}

func validCredentialValue(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 1024 && !bytes.ContainsAny([]byte(value), "\r\n\t")
}

func credentialStoreError(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrCredential
}
