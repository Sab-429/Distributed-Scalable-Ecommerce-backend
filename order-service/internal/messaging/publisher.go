package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Publisher struct {
	channel *amqp.Channel
}

func NewPublisher(channel *amqp.Channel) *Publisher {
	return &Publisher{channel: channel}
}

// EnableConfirms switches the channel to confirm mode so publishes are only
// considered successful after RabbitMQ acknowledges them.
func (p *Publisher) EnableConfirms() error {
	if err := p.channel.Confirm(false); err != nil {
		return fmt.Errorf("enable publisher confirms: %w", err)
	}
	return nil
}

// Setup declares all exchanges, queues, and bindings needed by the publisher.
func (p *Publisher) Setup() error {
	// ── Main exchange (topic) ────────────────────────────────────────────────
	if err := p.channel.ExchangeDeclare(
		OrderEventsExchange, "topic",
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,
	); err != nil {
		return fmt.Errorf("declare order exchange: %w", err)
	}

	// ── Dead-letter exchange (topic) ─────────────────────────────────────────
	if err := p.channel.ExchangeDeclare(
		OrderDLX, "topic",
		true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare DLX: %w", err)
	}

	// ── Retry exchange (topic) ──────────────────────────────────────────────
	if err := p.channel.ExchangeDeclare(
		OrderRetryExchange, "topic",
		true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare retry exchange: %w", err)
	}

	// ── Notification queue (main consumer queue with DLX args) ──────────────
	notifArgs := amqp.Table{
		"x-dead-letter-exchange": OrderDLX,
		// NOTE: no x-dead-letter-routing-key — the original routing key is preserved.
	}
	if _, err := p.channel.QueueDeclare(
		NotificationQueue,
		true,  // durable
		false, // auto-deleted
		false, // exclusive
		false, // no-wait
		notifArgs,
	); err != nil {
		return fmt.Errorf("declare notification queue: %w", err)
	}

	// Bind notification queue to main exchange for all order events.
	if err := p.channel.QueueBind(
		NotificationQueue,
		"order.#",
		OrderEventsExchange,
		false, nil,
	); err != nil {
		return fmt.Errorf("bind notification queue: %w", err)
	}

	// ── Dead-letter queue ────────────────────────────────────────────────────
	if _, err := p.channel.QueueDeclare(
		OrderDLQ,
		true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare DLQ: %w", err)
	}
	if err := p.channel.QueueBind(
		OrderDLQ, "#", OrderDLX, false, nil,
	); err != nil {
		return fmt.Errorf("bind DLQ: %w", err)
	}

	// ── Retry queue (with TTL + DLX back to main exchange) ───────────────────
	retryArgs := amqp.Table{
		"x-dead-letter-exchange": OrderEventsExchange,
		// NOTE: no x-dead-letter-routing-key — the original routing key (e.g.
		// "order.created") is preserved so the message re-routes to its queue.
		"x-message-ttl": int32(30000), // 30 seconds before retry
	}
	if _, err := p.channel.QueueDeclare(
		OrderRetryQueue,
		true, false, false, false,
		retryArgs,
	); err != nil {
		return fmt.Errorf("declare retry queue: %w", err)
	}
	if err := p.channel.QueueBind(
		OrderRetryQueue, "#", OrderRetryExchange, false, nil,
	); err != nil {
		return fmt.Errorf("bind retry queue: %w", err)
	}

	// Enable publisher confirms so the caller only succeeds after the broker
	// acknowledges each published message.
	if err := p.EnableConfirms(); err != nil {
		return fmt.Errorf("enable publisher confirms: %w", err)
	}
	return nil
}

// PublishOrderCreated publishes an order.created event to the main exchange.
func (p *Publisher) PublishOrderCreated(ctx context.Context, event OrderCreatedEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal order.created event: %w", err)
	}

	dc, err := p.channel.PublishWithDeferredConfirmWithContext(
		ctx,
		OrderEventsExchange,
		OrderCreatedRoutingKey,
		false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    event.EventID,
			Type:         event.EventType,
			Timestamp:    time.Now(),
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("publish order.created: %w", err)
	}

	// Block until RabbitMQ acknowledges the message, so a failure here
	// (broker down, queue deleted, etc.) is surfaced to the caller instead
	// of silently losing the event.
	ack, err := dc.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("confirm order.created publish: %w", err)
	}
	if !ack {
		return fmt.Errorf("confirm order.created publish: broker nacked message")
	}
	return nil
}

// PublishOrderCompleted publishes an order.completed event to the main exchange.
func (p *Publisher) PublishOrderCompleted(ctx context.Context, event OrderCompletedEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal order.completed event: %w", err)
	}

	dc, err := p.channel.PublishWithDeferredConfirmWithContext(
		ctx,
		OrderEventsExchange,
		OrderCompletedRoutingKey,
		false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    event.EventID,
			Type:         event.EventType,
			Timestamp:    time.Now(),
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("publish order.completed: %w", err)
	}

	// Block until RabbitMQ acknowledges the message, so a failure here
	// (broker down, queue deleted, etc.) is surfaced to the caller instead
	// of silently losing the event.
	ack, err := dc.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("confirm order.completed publish: %w", err)
	}
	if !ack {
		return fmt.Errorf("confirm order.completed publish: broker nacked message")
	}
	return nil
}

// PublishOrderCancelled publishes an order.cancelled event to the main exchange.
func (p *Publisher) PublishOrderCancelled(ctx context.Context, event OrderCancelledEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal order.cancelled event: %w", err)
	}

	dc, err := p.channel.PublishWithDeferredConfirmWithContext(
		ctx,
		OrderEventsExchange,
		OrderCancelledRoutingKey,
		false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    event.EventID,
			Type:         event.EventType,
			Timestamp:    time.Now(),
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("publish order.cancelled: %w", err)
	}

	// Block until RabbitMQ acknowledges the message, so a failure here
	// (broker down, queue deleted, etc.) is surfaced to the caller instead
	// of silently losing the event.
	ack, err := dc.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("confirm order.cancelled publish: %w", err)
	}
	if !ack {
		return fmt.Errorf("confirm order.cancelled publish: broker nacked message")
	}
	return nil
}
