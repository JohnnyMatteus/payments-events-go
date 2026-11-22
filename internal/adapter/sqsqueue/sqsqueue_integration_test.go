//go:build integration

package sqsqueue_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/sqsqueue"
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

// Um extrato depositado no bucket chega à fila como notificação e some dela
// depois do Ack.
func TestS3NotificationReachesQueue(t *testing.T) {
	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s3c := s3.NewFromConfig(awsCfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	sqsc := sqs.NewFromConfig(awsCfg, func(o *sqs.Options) { o.BaseEndpoint = aws.String(endpoint) })

	const bucket = "pagafacil-statements"
	if _, err := s3c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	q, err := sqsc.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("statements-events")})
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": []map[string]any{{
		"Effect": "Allow", "Principal": "*", "Action": "sqs:SendMessage",
		"Resource": "arn:aws:sqs:us-east-1:000000000000:statements-events",
	}}})
	if _, err := sqsc.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{
		QueueUrl: q.QueueUrl, Attributes: map[string]string{string(sqstypes.QueueAttributeNamePolicy): string(policy)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s3c.PutBucketNotificationConfiguration(ctx, &s3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String(bucket),
		NotificationConfiguration: &s3types.NotificationConfiguration{QueueConfigurations: []s3types.QueueConfiguration{{
			QueueArn: aws.String("arn:aws:sqs:us-east-1:000000000000:statements-events"),
			Events:   []s3types.Event{s3types.EventS3ObjectCreated},
			Filter: &s3types.NotificationConfigurationFilter{Key: &s3types.S3KeyFilter{FilterRules: []s3types.FilterRule{
				{Name: s3types.FilterRuleNamePrefix, Value: aws.String("settlements/")},
			}}},
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s3c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("settlements/2026-11-20.csv"), Body: strings.NewReader("transaction_id\n"),
	}); err != nil {
		t.Fatal(err)
	}

	queue, err := sqsqueue.New(ctx, sqsqueue.Config{
		Endpoint: endpoint, Region: "us-east-1", QueueURL: aws.ToString(q.QueueUrl), WaitSeconds: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		batch, err := queue.Receive(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range batch {
			if len(n.Events) == 1 && n.Events[0].Key == "settlements/2026-11-20.csv" && n.Events[0].Bucket == bucket {
				if err := queue.Ack(ctx, n); err != nil {
					t.Fatalf("Ack: %v", err)
				}
				return
			}
		}
	}
	t.Fatal("notification for the uploaded statement never arrived")
}
