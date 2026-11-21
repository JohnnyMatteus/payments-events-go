package objectstore

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenLocalFileAndRejectsBadS3Reference(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	o, err := New(context.Background(), S3Config{Region: "us-east-1", Endpoint: "http://127.0.0.1:1", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "extrato.csv")
	if err := os.WriteFile(path, []byte("conteudo"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, err := o.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(b) != "conteudo" {
		t.Fatalf("read %q", b)
	}

	for _, ref := range []string{"s3://", "s3://bucket", "s3://bucket/", "s3:///key"} {
		if _, err := o.Open(context.Background(), ref); err == nil {
			t.Errorf("Open(%q) should fail", ref)
		}
	}
	if _, err := o.Open(context.Background(), filepath.Join(t.TempDir(), "missing.csv")); err == nil {
		t.Fatal("missing local file must fail")
	}
}
