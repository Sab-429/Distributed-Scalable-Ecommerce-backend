package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"observability"
)

type Publisher struct {
	channel *amqp.Channel
}

func NewPublisher(channel *amqp.Channel) *Publisher {
	return &Publisher{channel: channel}
}

// Setup declares the exchange this service publishes to and switches the
// channel to confirm mode so a publish only succeeds once the broker
// acknowledges it.
func (p *Publisher) Setup() error {
	if err := p.channel.ExchangeDeclare(
		PaymentEventsExchange, "topic",
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,
	); err != nil {
		return fmt.Errorf("declare payment exchange: %w", err)
	}

	if err := p.channel.Confirm(false); err != nil {
		return fmt.Errorf("enable publisher confirms: %w", err)
	}

	return nil
}

// publish sends payload to routingKey and blocks until the broker confirms it.
// It opens an OpenTelemetry producer span and injects the W3C trace context
// into the AMQP headers, so consumers continue this trace.
func (p *Publisher) publish(
	ctx context.Context,
	routingKey string,
	payload any,
) (err error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s event: %w", routingKey, err)
	}

	info := telemetry.MessageInfo{
		Exchange:   PaymentEventsExchange,
		RoutingKey: routingKey,
		MessageID:  routingKey + "-" + uuidFrom(payload),
		EventType:  routingKey,
	}
	ctx, span, headers := telemetry.StartPublish(ctx, info, nil)
	start := time.Now()
	defer func() { telemetry.FinishPublish(ctx, span, start, info, err) }()

	dc, err := p.channel.PublishWithDeferredConfirmWithContext(
		ctx,
		PaymentEventsExchange,
		routingKey,
		false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    info.MessageID,
			Type:         routingKey,
			Timestamp:    time.Now(),
			Headers:      headers,
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("publish %s: %w", routingKey, err)
	}

	// Block until RabbitMQ acknowledges the message, so a failure here
	// (broker down, queue deleted, etc.) is surfaced to the caller instead
	// of silently losing the event.
	ack, err := dc.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("confirm %s publish: %w", routingKey, err)
	}
	if !ack {
		return fmt.Errorf("confirm %s publish: broker nacked message", routingKey)
	}
	return nil
}

// PublishPaymentCompleted publishes a payment.completed event.
func (p *Publisher) PublishPaymentCompleted(ctx context.Context, event PaymentCompletedEvent) error {
	if err := p.publish(ctx, PaymentCompletedRoutingKey, event); err != nil {
		return fmt.Errorf("payment.completed: %w", err)
	}
	return nil
}

// PublishPaymentFailed publishes a payment.failed event.
func (p *Publisher) PublishPaymentFailed(ctx context.Context, event PaymentFailedEvent) error {
	if err := p.publish(ctx, PaymentFailedRoutingKey, event); err != nil {
		return fmt.Errorf("payment.failed: %w", err)
	}
	return nil
}

// uuidFrom digs the event id out of an event struct so the AMQP MessageId is
// always the business event id (useful for tracing across services).
func uuidFrom(payload any) string {
	switch e := payload.(type) {
	case PaymentCompletedEvent:
		return e.EventID
	case PaymentFailedEvent:
		return e.EventID
	default:
		return ""
	}
}

// PublishRaw publishes a pre-serialized event to the payment.events exchange
// using publisher confirms. It is used by the outbox relay, which provides
// the routing key, event ID, event type, and already-marshaled payload.
func (p *Publisher) PublishRaw(
	ctx context.Context,
	routingKey string,
	eventID string,
	eventType string,
	payload []byte,
) (err error) {
	info := telemetry.MessageInfo{
		Exchange:   PaymentEventsExchange,
		RoutingKey: routingKey,
		MessageID:  eventID,
		EventType:  eventType,
	}
	ctx, span, headers := telemetry.StartPublish(ctx, info, nil)
	start := time.Now()
	defer func() { telemetry.FinishPublish(ctx, span, start, info, err) }()

	dc, err := p.channel.PublishWithDeferredConfirmWithContext(
		ctx,
		PaymentEventsExchange,
		routingKey,
		false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    eventID,
			Type:         eventType,
			Timestamp:    time.Now(),
			Headers:      headers,
			Body:         payload,
		},
	)
	if err != nil {
		return fmt.Errorf("publish outbox event %s: %w", eventID, err)
	}

	ack, err := dc.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("confirm outbox event %s publish: %w", eventID, err)
	}
	if !ack {
		return fmt.Errorf("confirm outbox event %s publish: broker nacked message", eventID)
	}
	return nil
}
