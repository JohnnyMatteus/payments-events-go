//go:build integration

package objectstore_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/objectstore"
	"github.com/JohnnyMatteus/payments-events-go/internal/testinfra"
)

var endpoint string

func TestMain(m *testing.M) {
	os.Exit(testinfra.Run(m, func(ctx context.Context) (func(), error) {
		ep, teardown, err := testinfra.StartLocalStack(ctx)
		endpoint = ep
		return teardown, err
	}))
}

func TestOpenReadsObjectFromS3(t *testing.T) {
	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("pagafacil-statements")}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("pagafacil-statements"), Key: aws.String("2026/11/20.csv"),
		Body: strings.NewReader("transaction_id,payment_id\n"),
	}); err != nil {
		t.Fatal(err)
	}

	opener, err := objectstore.New(ctx, objectstore.S3Config{Endpoint: endpoint, Region: "us-east-1", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	rc, err := opener.Open(ctx, "s3://pagafacil-statements/2026/11/20.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if string(body) != "transaction_id,payment_id\n" {
		t.Fatalf("body = %q", body)
	}

	if _, err := opener.Open(ctx, "s3://pagafacil-statements/missing.csv"); err == nil {
		t.Fatal("missing key must fail")
	}
}
