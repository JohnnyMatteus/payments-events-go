package rabbitmq

import (
	"slices"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestHeaderCarrier(t *testing.T) {
	t.Parallel()
	c := headerCarrier(amqp.Table{"number": int64(1)})
	c.Set("traceparent", "00-abc-def-01")
	if got := c.Get("traceparent"); got != "00-abc-def-01" {
		t.Fatalf("Get = %q", got)
	}
	if got := c.Get("number"); got != "" {
		t.Fatalf("non-string header must read as empty, got %q", got)
	}
	keys := c.Keys()
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"number", "traceparent"}) {
		t.Fatalf("Keys = %v", keys)
	}
}

func TestDeliveryCount(t *testing.T) {
	t.Parallel()
	cases := []struct {
		headers amqp.Table
		want    int
	}{
		{headers: nil, want: 0},
		{headers: amqp.Table{"x-delivery-count": int64(3)}, want: 3},
		{headers: amqp.Table{"x-delivery-count": int32(2)}, want: 2},
		{headers: amqp.Table{"x-delivery-count": "x"}, want: 0},
	}
	for _, tc := range cases {
		if got := deliveryCount(amqp.Delivery{Headers: tc.headers}); got != tc.want {
			t.Errorf("deliveryCount(%v) = %d, want %d", tc.headers, got, tc.want)
		}
	}
}
