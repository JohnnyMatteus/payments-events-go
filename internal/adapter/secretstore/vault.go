package secretstore

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	vault "github.com/hashicorp/vault/api"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// VaultConfig configura o acesso ao Vault (KV v2).
type VaultConfig struct {
	Addr  string
	Mount string // montagem do KV v2; padrão "secret"
	// Token estático: só para dev. Em produção use Role (auth Kubernetes).
	Token string
	// Role e JWTPath ligam o login por auth/kubernetes com o token da service account.
	Role    string
	JWTPath string
}

// Vault lê campos de segredos KV v2. Referência: "caminho#campo".
type Vault struct {
	client *vault.Client
	mount  string
	cfg    VaultConfig
	mu     sync.Mutex
}

var _ app.SecretStore = (*Vault)(nil)

// NewVault cria o cliente e, com Role, faz o login inicial (falha fechado).
func NewVault(ctx context.Context, cfg VaultConfig) (*Vault, error) {
	if cfg.Addr == "" {
		return nil, errors.New("vault: VAULT_ADDR é obrigatório")
	}
	if cfg.Token == "" && cfg.Role == "" {
		return nil, errors.New("vault: informe VAULT_TOKEN (dev) ou VAULT_K8S_ROLE")
	}
	if cfg.Mount == "" {
		cfg.Mount = "secret"
	}
	if cfg.JWTPath == "" {
		cfg.JWTPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}
	vc := vault.DefaultConfig()
	vc.Address = cfg.Addr
	client, err := vault.NewClient(vc)
	if err != nil {
		return nil, fmt.Errorf("vault client: %w", err)
	}
	v := &Vault{client: client, mount: cfg.Mount, cfg: cfg}
	if cfg.Token != "" {
		client.SetToken(cfg.Token)
		return v, nil
	}
	if err := v.login(ctx); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *Vault) login(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	jwt, err := os.ReadFile(v.cfg.JWTPath)
	if err != nil {
		return fmt.Errorf("vault: read service account token: %w", err)
	}
	secret, err := v.client.Logical().WriteWithContext(ctx, "auth/kubernetes/login", map[string]any{
		"role": v.cfg.Role, "jwt": strings.TrimSpace(string(jwt)),
	})
	if err != nil {
		return fmt.Errorf("vault kubernetes login: %w", err)
	}
	if secret == nil || secret.Auth == nil || secret.Auth.ClientToken == "" {
		return errors.New("vault kubernetes login: resposta sem token")
	}
	v.client.SetToken(secret.Auth.ClientToken)
	return nil
}

// Get devolve o campo do segredo. Se o token expirou (403) e há Role, entra de
// novo uma vez e repete.
func (v *Vault) Get(ctx context.Context, ref string) (string, error) {
	path, field := splitRef(ref)
	if path == "" || field == "" {
		return "", fmt.Errorf("vault: referência %q deve ser caminho#campo", ref)
	}
	val, err := v.read(ctx, path, field)
	var respErr *vault.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == http.StatusForbidden && v.cfg.Role != "" {
		if lerr := v.login(ctx); lerr != nil {
			return "", lerr
		}
		return v.read(ctx, path, field)
	}
	return val, err
}

func (v *Vault) read(ctx context.Context, path, field string) (string, error) {
	sec, err := v.client.KVv2(v.mount).Get(ctx, path)
	if err != nil {
		return "", fmt.Errorf("vault read %s: %w", path, err)
	}
	val, ok := sec.Data[field].(string)
	if !ok {
		return "", fmt.Errorf("vault %s has no string field %q", path, field)
	}
	return val, nil
}
