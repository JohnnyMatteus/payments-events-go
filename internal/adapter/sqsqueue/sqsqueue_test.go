package sqsqueue

import "testing"

func TestParseS3Event(t *testing.T) {
	body := []byte(`{"Records":[{"eventName":"ObjectCreated:Put","s3":{"bucket":{"name":"pagafacil-statements"},
		"object":{"key":"settlements/2026-11-20.csv","versionId":"abc"}}},
		{"s3":{"bucket":{"name":"b"},"object":{"key":"a+b%2Fc.csv"}}}]}`)
	events, err := ParseS3Event(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Bucket != "pagafacil-statements" || events[0].Key != "settlements/2026-11-20.csv" ||
		events[0].VersionID != "abc" {
		t.Fatalf("events = %+v", events)
	}
	if events[1].Key != "a b/c.csv" {
		t.Fatalf("key = %q, want decoded", events[1].Key)
	}
}

func TestParseS3EventTestEventHasNoRecords(t *testing.T) {
	events, err := ParseS3Event([]byte(`{"Service":"Amazon S3","Event":"s3:TestEvent"}`))
	if err != nil || len(events) != 0 {
		t.Fatalf("events = %v, err = %v", events, err)
	}
	if _, err := ParseS3Event([]byte(`not json`)); err == nil {
		t.Fatal("invalid body must fail")
	}
}
