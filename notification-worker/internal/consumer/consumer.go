package consumer

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
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

	log.Println("RabbitMQ connected")
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

func (c *Consumer) process(d amqp.Delivery) {
	ctx := context.Background()

	if err := c.handle(ctx, d); err != nil {
		retries := retryCount(d)
		if isUnprocessable(err) || int(retries) >= c.maxRetries {
			log.Printf("[consumer] giving up on message %s after %d retries: %v → DLQ",
				d.MessageId, retries, err)
			_ = d.Nack(false, false) // dead-letters to OrderDLQ
			return
		}

		// Republish to the retry exchange with an incremented retry counter,
		// then ack the original so it doesn't double-deliver.
		if err := c.republishForRetry(d, retries+1); err != nil {
			log.Printf("[consumer] failed to republish for retry: %v → Nack(requeue=false)", err)
			_ = d.Nack(false, false)
			return
		}
		log.Printf("[consumer] retrying message %s (attempt %d/%d)", d.MessageId, retries+1, c.maxRetries)
		_ = d.Ack(false)
		return
	}

	_ = d.Ack(false)
}

func (c *Consumer) republishForRetry(d amqp.Delivery, retry int32) error {
	headers := amqp.Table{}
	for k, v := range d.Headers {
		headers[k] = v
	}
	headers["x-retry-count"] = retry

	return c.channel.PublishWithContext(
		context.Background(),
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
