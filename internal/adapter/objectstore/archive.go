package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// Archive implementa app.ArchiveWriter: guarda as exportações do ledger num
// bucket próprio (versionado), separado do bucket de extratos do provedor.
type Archive struct {
	s3     *s3.Client
	bucket string
}

var _ app.ArchiveWriter = (*Archive)(nil)

// Archive devolve o escritor de arquivo do bucket informado.
func (o *Opener) Archive(bucket string) *Archive {
	return &Archive{s3: o.s3, bucket: bucket}
}

// PutArchive grava o objeto com o sha256 nos metadados e devolve s3://bucket/chave.
func (a *Archive) PutArchive(ctx context.Context, key string, body []byte) (string, error) {
	sum := sha256.Sum256(body)
	_, err := a.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(a.bucket), Key: aws.String(key), Body: bytes.NewReader(body),
		ContentType: aws.String("text/csv"), Metadata: map[string]string{hashMetadataKey: hex.EncodeToString(sum[:])},
	})
	if err != nil {
		return "", fmt.Errorf("put s3://%s/%s: %w", a.bucket, key, err)
	}
	return "s3://" + a.bucket + "/" + key, nil
}
