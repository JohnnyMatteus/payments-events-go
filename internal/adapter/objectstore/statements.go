package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

const (
	hashMetadataKey = "sha256"
	maxListed       = 60
)

// Statements implementa app.StatementStore: um extrato por dia em
// settlements/AAAA-MM-DD.csv no bucket do provedor.
type Statements struct {
	s3     *s3.Client
	bucket string
}

// Statements devolve o repositório de extratos do bucket informado.
func (o *Opener) Statements(bucket string) *Statements {
	return &Statements{s3: o.s3, bucket: bucket}
}

// Put grava o extrato do dia, substituindo o anterior (o bucket é versionado).
func (s *Statements) Put(ctx context.Context, date time.Time, body []byte) (app.StatementObject, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	key := app.SettlementKey(date)
	_, err := s.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(body),
		ContentType: aws.String("text/csv"), Metadata: map[string]string{hashMetadataKey: hash},
	})
	if err != nil {
		return app.StatementObject{}, fmt.Errorf("put s3://%s/%s: %w", s.bucket, key, err)
	}
	return s.Stat(ctx, date)
}

// Stat devolve os dados do extrato do dia; app.ErrStatementMissing se não existe.
func (s *Statements) Stat(ctx context.Context, date time.Time) (app.StatementObject, error) {
	return s.statKey(ctx, app.SettlementKey(date))
}

func (s *Statements) statKey(ctx context.Context, key string) (app.StatementObject, error) {
	date, ok := app.SettlementDateFromKey(key)
	if !ok {
		return app.StatementObject{}, fmt.Errorf("%w: key %s", app.ErrInvalidInput, key)
	}
	head, err := s.s3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var notFound *types.NotFound
		if errors.As(err, &notFound) {
			return app.StatementObject{}, app.ErrStatementMissing
		}
		return app.StatementObject{}, fmt.Errorf("head s3://%s/%s: %w", s.bucket, key, err)
	}
	obj := app.StatementObject{
		Date: date, Ref: "s3://" + s.bucket + "/" + key, Size: aws.ToInt64(head.ContentLength),
		SHA256: head.Metadata[hashMetadataKey], LastModified: aws.ToTime(head.LastModified),
	}
	if obj.SHA256 == "" {
		// Arquivo depositado fora da API (o provedor): calcula o hash uma vez.
		if obj.SHA256, err = s.hash(ctx, key); err != nil {
			return app.StatementObject{}, err
		}
	}
	return obj, nil
}

func (s *Statements) hash(ctx context.Context, key string) (string, error) {
	out, err := s.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return "", fmt.Errorf("get s3://%s/%s: %w", s.bucket, key, err)
	}
	defer func() { _ = out.Body.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, out.Body); err != nil {
		return "", fmt.Errorf("hash s3://%s/%s: %w", s.bucket, key, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// List devolve os extratos mais recentes, do dia mais novo para o mais antigo.
func (s *Statements) List(ctx context.Context) ([]app.StatementObject, error) {
	var keys []string
	pages := s3.NewListObjectsV2Paginator(s.s3, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket), Prefix: aws.String(app.SettlementPrefix),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list s3://%s/%s: %w", s.bucket, app.SettlementPrefix, err)
		}
		for _, o := range page.Contents {
			if _, ok := app.SettlementDateFromKey(aws.ToString(o.Key)); ok {
				keys = append(keys, aws.ToString(o.Key))
			}
		}
	}
	slices.Sort(keys)
	slices.Reverse(keys)
	keys = keys[:min(len(keys), maxListed)]
	out := make([]app.StatementObject, 0, len(keys))
	for _, k := range keys {
		obj, err := s.statKey(ctx, k)
		if err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	return out, nil
}
