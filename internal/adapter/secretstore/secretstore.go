// Package secretstore lê segredos de um cofre (Vault ou AWS Secrets Manager,
// este também no LocalStack) atrás da porta app.SecretStore.
package secretstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// splitRef separa "id#campo". Sem campo, o valor inteiro é o segredo.
func splitRef(ref string) (id, field string) {
	id, field, _ = strings.Cut(ref, "#")
	return strings.TrimSpace(id), strings.TrimSpace(field)
}

// fieldOf extrai um campo string de um JSON de segredo.
func fieldOf(raw, field, ref string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return "", fmt.Errorf("secret %s is not a JSON object, cannot read field %q", ref, field)
	}
	v, ok := m[field].(string)
	if !ok {
		return "", fmt.Errorf("secret %s has no string field %q", ref, field)
	}
	return v, nil
}

var errNoRef = errors.New("secret reference is empty")
