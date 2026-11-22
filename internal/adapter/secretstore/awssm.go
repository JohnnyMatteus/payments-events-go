package secretstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// AWSConfig configura o Secrets Manager. Endpoint vazio usa o da AWS; no
// LocalStack aponte para o emulador. As credenciais seguem a cadeia do SDK.
type AWSConfig struct {
	Endpoint string
	Region   string
}

// AWSSecretsManager lê segredos do Secrets Manager. Referência: "id" (valor
// inteiro) ou "id#campo" (campo de um segredo JSON).
type AWSSecretsManager struct {
	client *secretsmanager.Client
}

var _ app.SecretStore = (*AWSSecretsManager)(nil)

// NewAWSSecretsManager cria o cliente. Nenhuma chamada de rede é feita aqui.
func NewAWSSecretsManager(ctx context.Context, cfg AWSConfig) (*AWSSecretsManager, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := secretsmanager.NewFromConfig(awsCfg, func(o *secretsmanager.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return &AWSSecretsManager{client: client}, nil
}

// Get devolve o segredo (a versão AWSCURRENT).
func (a *AWSSecretsManager) Get(ctx context.Context, ref string) (string, error) {
	id, field := splitRef(ref)
	if id == "" {
		return "", errNoRef
	}
	out, err := a.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(id)})
	if err != nil {
		return "", fmt.Errorf("secrets manager get %s: %w", id, err)
	}
	if out.SecretString == nil {
		return "", errors.New("secrets manager: segredo binário não é suportado")
	}
	if field == "" {
		return *out.SecretString, nil
	}
	return fieldOf(*out.SecretString, field, ref)
}
