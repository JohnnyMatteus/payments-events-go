// Package sqsqueue consome as notificações de objeto criado do S3 entregues
// numa fila SQS (no LocalStack em desenvolvimento).
package sqsqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// Config configura o cliente SQS. Endpoint vazio usa o endpoint padrão da AWS.
type Config struct {
	Endpoint string
	Region   string
	QueueURL string
	// WaitSeconds é o long polling; MaxMessages limita o lote por leitura.
	WaitSeconds int32
	MaxMessages int32
}

// Queue implementa app.ObjectNotifier.
type Queue struct {
	client *sqs.Client
	cfg    Config
}

// New cria o cliente. Nenhuma chamada de rede é feita aqui.
func New(ctx context.Context, cfg Config) (*Queue, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	if cfg.WaitSeconds == 0 {
		cfg.WaitSeconds = 10
	}
	if cfg.MaxMessages == 0 {
		cfg.MaxMessages = 5
	}
	client := sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return &Queue{client: client, cfg: cfg}, nil
}

// Receive lê um lote com long polling. Mensagens que não são notificações de
// objeto (como o s3:TestEvent) voltam sem eventos, e o consumidor as confirma.
func (q *Queue) Receive(ctx context.Context) ([]app.ObjectNotification, error) {
	out, err := q.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl: aws.String(q.cfg.QueueURL), MaxNumberOfMessages: q.cfg.MaxMessages, WaitTimeSeconds: q.cfg.WaitSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("receive from %s: %w", q.cfg.QueueURL, err)
	}
	notifications := make([]app.ObjectNotification, 0, len(out.Messages))
	for _, m := range out.Messages {
		events, err := ParseS3Event([]byte(aws.ToString(m.Body)))
		if err != nil {
			// Corpo ilegível não melhora com nova tentativa: segue para a DLQ da fila.
			continue
		}
		notifications = append(notifications, app.ObjectNotification{Receipt: aws.ToString(m.ReceiptHandle), Events: events})
	}
	return notifications, nil
}

// Ack apaga a mensagem da fila.
func (q *Queue) Ack(ctx context.Context, n app.ObjectNotification) error {
	_, err := q.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl: aws.String(q.cfg.QueueURL), ReceiptHandle: aws.String(n.Receipt),
	})
	if err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	return nil
}

type s3Notification struct {
	Records []struct {
		EventName string `json:"eventName"`
		S3        struct {
			Bucket struct {
				Name string `json:"name"`
			} `json:"bucket"`
			Object struct {
				Key       string `json:"key"`
				VersionID string `json:"versionId"`
			} `json:"object"`
		} `json:"s3"`
	} `json:"Records"`
}

// ParseS3Event extrai os objetos criados de uma notificação do S3. A chave
// chega codificada como URL (espaços viram "+").
func ParseS3Event(body []byte) ([]app.ObjectEvent, error) {
	var n s3Notification
	if err := json.Unmarshal(body, &n); err != nil {
		return nil, fmt.Errorf("decode s3 notification: %w", err)
	}
	events := make([]app.ObjectEvent, 0, len(n.Records))
	for _, r := range n.Records {
		key, err := url.QueryUnescape(r.S3.Object.Key)
		if err != nil {
			return nil, fmt.Errorf("decode object key %q: %w", r.S3.Object.Key, err)
		}
		events = append(events, app.ObjectEvent{Bucket: r.S3.Bucket.Name, Key: key, VersionID: r.S3.Object.VersionID})
	}
	return events, nil
}
