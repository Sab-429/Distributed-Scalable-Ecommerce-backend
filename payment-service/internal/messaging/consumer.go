package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// EventHandler processes order events. It is implemented by
// service.PaymentService; declaring it here keeps this package free of any
// dependency on the service layer (which itself depends on this package for
// publishing) and therefore avoids an import cycle.
type EventHandler interface {
	HandleOrderEvent(ctx context.Context, event OrderEvent) error
}

// Consumer consumes order.* events from payment.order.events with a
// retry / dead-letter strategy. Retry/DLQ topology mirrors the
// notification-worker.
type Consumer struct {
	rabbit     *RabbitMQ
	handler    EventHandler
	maxRetries int
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

func NewConsumer(rabbit *RabbitMQ, handler EventHandler, maxRetries int) (*Consumer, error) {
	// Fair dispatch: a bounded number of unacknowledged messages per consumer.
	if err := rabbit.Channel.Qos(10, 0, false); err != nil {
		return nil, fmt.Errorf("failed to set qos: %w", err)
	}

	return &Consumer{
		rabbit:     rabbit,
		handler:    handler,
		maxRetries: maxRetries,
	}, nil
}

// Setup declares exchanges, queues and bindings idempotently so the service can
// start independently of the order-service.
func (c *Consumer) Setup() error {
	ch := c.rabbit.Channel

	// The order.events exchange is owned by order-service; declaring it here
	// only makes the declaration idempotent (it fails only on a type/arg
	// mismatch).
	if err := ch.ExchangeDeclare(
		OrderEventsExchange, "topic", true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare order exchange: %w", err)
	}

	// ── Main consumer queue (dead-letters to our own DLX) ────────────────────
	if _, err := ch.QueueDeclare(
		PaymentOrderQueue,
		true,  // durable
		false, // auto-deleted
		false, // exclusive
		false, // no-wait
		amqp.Table{
			"x-dead-letter-exchange": PaymentOrderDLX,
			// NOTE: no x-dead-letter-routing-key — the original routing key
			// (e.g. "order.created") is preserved.
		},
	); err != nil {
		return fmt.Errorf("declare payment queue: %w", err)
	}
	if err := ch.QueueBind(
		PaymentOrderQueue, "order.#", OrderEventsExchange, false, nil,
	); err != nil {
		return fmt.Errorf("bind payment queue: %w", err)
	}

	// ── Dead-letter queue ────────────────────────────────────────────────────
	if err := ch.ExchangeDeclare(
		PaymentOrderDLX, "topic", true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare payment DLX: %w", err)
	}
	if _, err := ch.QueueDeclare(
		PaymentOrderDLQ, true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare payment DLQ: %w", err)
	}
	if err := ch.QueueBind(PaymentOrderDLQ, "#", PaymentOrderDLX, false, nil); err != nil {
		return fmt.Errorf("bind payment DLQ: %w", err)
	}

	// ── Retry queue (TTL + dead-letter back into order.events) ───────────────
	if err := ch.ExchangeDeclare(
		PaymentOrderRetryExchange, "topic", true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare payment retry exchange: %w", err)
	}
	if _, err := ch.QueueDeclare(
		PaymentOrderRetryQueue, true, false, false, false,
		amqp.Table{
			"x-dead-letter-exchange": OrderEventsExchange,
			"x-message-ttl":          int32(30000), // 30 seconds before retry
		},
	); err != nil {
		return fmt.Errorf("declare payment retry queue: %w", err)
	}
	if err := ch.QueueBind(
		PaymentOrderRetryQueue, "#", PaymentOrderRetryExchange, false, nil,
	); err != nil {
		return fmt.Errorf("bind payment retry queue: %w", err)
	}

	return nil
}

// Start begins consuming messages from payment.order.events.
func (c *Consumer) Start() error {
	deliveries, err := c.rabbit.Channel.Consume(
		PaymentOrderQueue,
		"",    // consumer tag (auto)
		false, // auto-ack — we ack manually
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,
	)
	if err != nil {
		return fmt.Errorf("start consuming: %w", err)
	}

	// Graceful shutdown: the delivery loop exits on stopCh or when the
	// channel is closed (e.g. connection drop), then Stop() waits for it.
	c.stopCh = make(chan struct{})
	c.wg = sync.WaitGroup{}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			select {
			case <-c.stopCh:
				return
			case d, ok := <-deliveries:
				if !ok {
					return
				}
				c.process(d)
			}
		}
	}()
	return nil
}

// Stop stops consuming, drains in-flight messages and closes the connection.
// It blocks until the delivery loop has exited.
func (c *Consumer) Stop() {
	if c.stopCh == nil {
		return
	}
	close(c.stopCh)
	// Cancel the consumer so the deliveries channel is closed and the loop
	// drains, then wait for the loop to finish.
	_ = c.rabbit.Channel.Cancel("", false)
	c.wg.Wait()
	c.rabbit.Close()
}

func (c *Consumer) process(d amqp.Delivery) {
	ctx := context.Background()

	if err := c.handle(ctx, d); err != nil {
		retries := retryCount(d)
		if isUnprocessable(err) || int(retries) >= c.maxRetries {
			log.Printf("[payment-consumer] giving up on message %s after %d retries: %v → DLQ",
				d.MessageId, retries, err)
			_ = d.Nack(false, false) // dead-letters to PaymentOrderDLQ
			return
		}

		// Republish to the retry exchange with an incremented retry counter,
		// then ack the original so it doesn't double-deliver.
		if err := c.republishForRetry(d, retries+1); err != nil {
			log.Printf("[payment-consumer] failed to republish for retry: %v → Nack(requeue=false)", err)
			_ = d.Nack(false, false)
			return
		}
		log.Printf("[payment-consumer] retrying message %s (attempt %d/%d)", d.MessageId, retries+1, c.maxRetries)
		_ = d.Ack(false)
		return
	}

	_ = d.Ack(false)
}

// handle decodes the shared envelope and delegates to the payment service.
func (c *Consumer) handle(ctx context.Context, d amqp.Delivery) error {
	var event OrderEvent
	if err := json.Unmarshal(d.Body, &event); err != nil {
		return fmt.Errorf("unmarshal event: %w", err)
	}

	if err := c.handler.HandleOrderEvent(ctx, event); err != nil {
		return fmt.Errorf("handle %s (order %d): %w", event.EventType, event.OrderID, err)
	}
	return nil
}

func (c *Consumer) republishForRetry(d amqp.Delivery, retry int32) error {
	headers := amqp.Table{}
	for k, v := range d.Headers {
		headers[k] = v
	}
	headers["x-retry-count"] = retry

	return c.rabbit.Channel.PublishWithContext(
		context.Background(),
		PaymentOrderRetryExchange,
		d.RoutingKey,
		false, false,
		amqp.Publishing{
			ContentType:  d.ContentType,
			DeliveryMode: amqp.Persistent,
			MessageId:    d.MessageId,
			Type:         d.Type,
			Timestamp:    d.Timestamp,
			Headers:      headers,
			Body:         d.Body,
		},
	)
}

func retryCount(d amqp.Delivery) int32 {
	if d.Headers == nil {
		return 0
	}
	switch v := d.Headers["x-retry-count"].(type) {
	case int32:
		return v
	case int64:
		return int32(v)
	case int:
		return int32(v)
	default:
		return 0
	}
}

// isUnprocessable reports whether the error can never succeed on retry
// (e.g. a payload that fails to parse).
func isUnprocessable(err error) bool {
	return strings.HasPrefix(err.Error(), "unmarshal event:")
}
