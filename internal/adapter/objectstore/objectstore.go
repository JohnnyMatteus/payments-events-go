// Package objectstore abre arquivos por referência: s3://bucket/chave no S3
// (ou em um emulador compatível, como o LocalStack) e caminhos locais no disco.
package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Config configura o cliente S3. Endpoint vazio usa o endpoint padrão da
// AWS; no LocalStack, aponte para o emulador e ligue UsePathStyle. As
// credenciais seguem a cadeia padrão do SDK (variáveis, perfil, IAM role).
type S3Config struct {
	Endpoint     string
	Region       string
	UsePathStyle bool
}

// Opener implementa csvstatement.Opener.
type Opener struct {
	s3 *s3.Client
}

// New cria o Opener e o cliente S3. Nenhuma chamada de rede é feita aqui.
func New(ctx context.Context, cfg S3Config) (*Opener, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})
	return &Opener{s3: client}, nil
}

// Open abre o arquivo. Referências s3:// vão para o S3; o resto é tratado
// como caminho local, útil para conciliar um arquivo recebido fora do fluxo.
func (o *Opener) Open(ctx context.Context, ref string) (io.ReadCloser, error) {
	rest, isS3 := strings.CutPrefix(ref, "s3://")
	if !isS3 {
		f, err := os.Open(ref) //nolint:gosec // caminho informado pelo operador na linha de comando
		if err != nil {
			return nil, fmt.Errorf("open local statement: %w", err)
		}
		return f, nil
	}
	bucket, key, ok := strings.Cut(rest, "/")
	if !ok || bucket == "" || key == "" {
		return nil, errors.New("s3 reference must be s3://bucket/key")
	}
	out, err := o.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return nil, fmt.Errorf("get s3://%s/%s: %w", bucket, key, err)
	}
	return out.Body, nil
}
