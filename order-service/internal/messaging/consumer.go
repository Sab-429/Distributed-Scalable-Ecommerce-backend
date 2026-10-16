package messaging

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// OrderStore is the slice of the order repository this package needs. It is
// implemented by *repository.OrderRepository.
type OrderStore interface {
	// ConfirmOrder moves a pending order to confirmed and reports whether a
	// row was updated. false means the order was already settled (or is
	// cancelled/completed) — a no-op for this consumer.
	ConfirmOrder(ctx context.Context, orderID uint) (bool, error)
}

// PaymentConsumer consumes payment.* events from payment.order.events with a
// retry / dead-letter strategy (same shape as the notification-worker).
type PaymentConsumer struct {
	rabbit     *RabbitMQ
	store      OrderStore
	maxRetries int
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

func NewPaymentConsumer(rabbit *RabbitMQ, store OrderStore, maxRetries int) (*PaymentConsumer, error) {
	// Fair dispatch: a bounded number of unacknowledged messages per consumer.
	if err := rabbit.Channel.Qos(10, 0, false); err != nil {
		return nil, fmt.Errorf("failed to set qos: %w", err)
	}

	return &PaymentConsumer{
		rabbit:     rabbit,
		store:      store,
		maxRetries: maxRetries,
	}, nil
}

// Setup declares exchanges/queues/bindings idempotently so the order-service
// can start independently of the payment-service.
func (c *PaymentConsumer) Setup() error {
	ch := c.rabbit.Channel

	// The payment.events exchange is owned by payment-service; declaring it
	// here only makes the declaration idempotent (it fails only on a
	// type/arg mismatch).
	if err := ch.ExchangeDeclare(
		PaymentEventsExchange, "topic", true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare payment exchange: %w", err)
	}

	// ── Main consumer queue (dead-letters to our own DLX) ────────────────────
	if _, err := ch.QueueDeclare(
		OrderPaymentQueue,
		true,  // durable
		false, // auto-deleted
		false, // exclusive
		false, // no-wait
		amqp.Table{
			"x-dead-letter-exchange": OrderPaymentDLX,
			// NOTE: no x-dead-letter-routing-key — the original routing key
			// (e.g. "payment.completed") is preserved.
		},
	); err != nil {
		return fmt.Errorf("declare order payment queue: %w", err)
	}
	if err := ch.QueueBind(
		OrderPaymentQueue, "payment.#", PaymentEventsExchange, false, nil,
	); err != nil {
		return fmt.Errorf("bind order payment queue: %w", err)
	}

	// ── Dead-letter queue ────────────────────────────────────────────────────
	if err := ch.ExchangeDeclare(
		OrderPaymentDLX, "topic", true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare order payment DLX: %w", err)
	}
	if _, err := ch.QueueDeclare(
		OrderPaymentDLQ, true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare order payment DLQ: %w", err)
	}
	if err := ch.QueueBind(OrderPaymentDLQ, "#", OrderPaymentDLX, false, nil); err != nil {
		return fmt.Errorf("bind order payment DLQ: %w", err)
	}

	// ── Retry queue (TTL + dead-letter back into payment.events) ─────────────
	if err := ch.ExchangeDeclare(
		OrderPaymentRetryExchange, "topic", true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare order payment retry exchange: %w", err)
	}
	if _, err := ch.QueueDeclare(
		OrderPaymentRetryQueue, true, false, false, false,
		amqp.Table{
			"x-dead-letter-exchange": PaymentEventsExchange,
			"x-message-ttl":          int32(30000), // 30 seconds before retry
		},
	); err != nil {
		return fmt.Errorf("declare order payment retry queue: %w", err)
	}
	if err := ch.QueueBind(
		OrderPaymentRetryQueue, "#", OrderPaymentRetryExchange, false, nil,
	); err != nil {
		return fmt.Errorf("bind order payment retry queue: %w", err)
	}

	return nil
}

// Start begins consuming messages from order.payment.events.
func (c *PaymentConsumer) Start() error {
	deliveries, err := c.rabbit.Channel.Consume(
		OrderPaymentQueue,
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
func (c *PaymentConsumer) Stop() {
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

func (c *PaymentConsumer) process(d amqp.Delivery) {
	ctx := context.Background()

	if err := c.handle(ctx, d); err != nil {
		retries := retryCount(d)
		if isUnprocessable(err) || int(retries) >= c.maxRetries {
			log.Printf("[order-payment-consumer] giving up on message %s after %d retries: %v → DLQ",
				d.MessageId, retries, err)
			_ = d.Nack(false, false) // dead-letters to OrderPaymentDLQ
			return
		}

		// Republish to the retry exchange with an incremented retry counter,
		// then ack the original so it doesn't double-deliver.
		if err := c.republishForRetry(d, retries+1); err != nil {
			log.Printf("[order-payment-consumer] failed to republish for retry: %v → Nack(requeue=false)", err)
			_ = d.Nack(false, false)
			return
		}
		log.Printf("[order-payment-consumer] retrying message %s (attempt %d/%d)", d.MessageId, retries+1, c.maxRetries)
		_ = d.Ack(false)
		return
	}

	_ = d.Ack(false)
}

func (c *PaymentConsumer) republishForRetry(d amqp.Delivery, retry int32) error {
	headers := amqp.Table{}
	for k, v := range d.Headers {
		headers[k] = v
	}
	headers["x-retry-count"] = retry

	return c.rabbit.Channel.PublishWithContext(
		context.Background(),
		OrderPaymentRetryExchange,
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
