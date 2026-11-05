package consumer

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"observability"
)

// Consumer connects to RabbitMQ and processes order events from the
// notification queue with a retry / dead-letter strategy.
type Consumer struct {
	conn       *amqp.Connection
	channel    *amqp.Channel
	maxRetries int
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

func New(cfg Config) (*Consumer, error) {
	vhostPath := strings.TrimPrefix(cfg.VHost, "/")
	uri := url.URL{
		Scheme: "amqp",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   cfg.Host + ":" + cfg.Port,
		Path:   "/" + vhostPath,
	}

	conn, err := amqp.Dial(uri.String())
	if err != nil {
		return nil, fmt.Errorf("rabbitmq connection failed: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("rabbitmq channel creation failed: %w", err)
	}

	// Fair dispatch: one message at a time per consumer.
	if err := ch.Qos(10, 0, false); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to set qos: %w", err)
	}

	fmt.Println("RabbitMQ connected")
	return &Consumer{
		conn:       conn,
		channel:    ch,
		maxRetries: cfg.MaxRetries,
	}, nil
}

// Setup declares exchanges/queues/bindings idempotently so the worker can
// start independently of the order-service.
func (c *Consumer) Setup() error {
	for name, kind := range map[string]string{
		OrderEventsExchange: "topic",
		OrderDLX:            "topic",
		OrderRetryExchange:  "topic",
	} {
		if err := c.channel.ExchangeDeclare(name, kind, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare exchange %s: %w", name, err)
		}
	}

	if _, err := c.channel.QueueDeclare(NotificationQueue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange": OrderDLX,
	}); err != nil {
		return fmt.Errorf("declare notification queue: %w", err)
	}
	if err := c.channel.QueueBind(NotificationQueue, "order.#", OrderEventsExchange, false, nil); err != nil {
		return fmt.Errorf("bind notification queue: %w", err)
	}

	if _, err := c.channel.QueueDeclare(OrderDLQ, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare DLQ: %w", err)
	}
	if err := c.channel.QueueBind(OrderDLQ, "#", OrderDLX, false, nil); err != nil {
		return fmt.Errorf("bind DLQ: %w", err)
	}

	if _, err := c.channel.QueueDeclare(OrderRetryQueue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange": OrderEventsExchange,
		"x-message-ttl":          int32(30000),
	}); err != nil {
		return fmt.Errorf("declare retry queue: %w", err)
	}
	if err := c.channel.QueueBind(OrderRetryQueue, "#", OrderRetryExchange, false, nil); err != nil {
		return fmt.Errorf("bind retry queue: %w", err)
	}

	return nil
}

// Start begins consuming messages from the notification queue.
func (c *Consumer) Start() error {
	deliveries, err := c.channel.Consume(
		NotificationQueue,
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

// Stop signals the consumer to stop consuming and releases RabbitMQ
// resources. It blocks until the delivery loop has exited.
func (c *Consumer) Stop() {
	close(c.stopCh)
	// Cancel the consumer so the deliveries channel is closed and the loop
	// drains, then wait for the loop to finish.
	_ = c.channel.Cancel("", false)
	c.wg.Wait()
	c.Close()
}

// process handles one delivery inside a consumer span linked to the producer's
// trace (W3C context is read from the AMQP headers) and records
// messaging.process.* metrics for success / retry / dead-letter outcomes.
func (c *Consumer) process(d amqp.Delivery) {
	info := telemetry.MessageInfo{
		Queue:      NotificationQueue,
		RoutingKey: d.RoutingKey,
		MessageID:  d.MessageId,
		EventType:  d.Type,
	}
	ctx, span := telemetry.StartConsume(context.Background(), info, d.Headers)
	start := time.Now()

	if err := c.handle(ctx, d); err != nil {
		retries := retryCount(d)
		if isUnprocessable(err) || int(retries) >= c.maxRetries {
			telemetry.L(ctx).ErrorContext(ctx, "giving up on message, dead-lettering",
				"queue", NotificationQueue,
				"message_id", d.MessageId,
				"retries", retries,
				"error", err,
			)
			telemetry.FinishConsume(ctx, span, start, info, telemetry.OutcomeDeadLetter, err)
			telemetry.RecordFailure(ctx, "notification-worker.consumer", "retries_exhausted", err)
			_ = d.Nack(false, false) // dead-letters to OrderDLQ
			return
		}

		// Republish to the retry exchange with an incremented retry counter,
		// then ack the original so it doesn't double-deliver.
		if rerr := c.republishForRetry(ctx, d, retries+1); rerr != nil {
			telemetry.L(ctx).ErrorContext(ctx, "failed to republish for retry",
				"queue", NotificationQueue,
				"message_id", d.MessageId,
				"error", rerr,
			)
			telemetry.FinishConsume(ctx, span, start, info, telemetry.OutcomeError, rerr)
			_ = d.Nack(false, false)
			return
		}
		telemetry.L(ctx).WarnContext(ctx, "retrying message",
			"queue", NotificationQueue,
			"message_id", d.MessageId,
			"attempt", retries+1,
			"max_retries", c.maxRetries,
		)
		telemetry.FinishConsume(ctx, span, start, info, telemetry.OutcomeRetry, err)
		_ = d.Ack(false)
		return
	}

	telemetry.FinishConsume(ctx, span, start, info, telemetry.OutcomeSuccess, nil)
	_ = d.Ack(false)
}

func (c *Consumer) republishForRetry(ctx context.Context, d amqp.Delivery, retry int32) error {
	headers := amqp.Table{}
	for k, v := range d.Headers {
		headers[k] = v
	}
	headers["x-retry-count"] = retry
	// Keep the trace alive across the retry hop.
	headers = telemetry.InjectTraceContext(ctx, headers)

	return c.channel.PublishWithContext(
		ctx,
		OrderRetryExchange,
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

func (c *Consumer) Close() {
	if c.channel != nil {
		_ = c.channel.Close()
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
}
