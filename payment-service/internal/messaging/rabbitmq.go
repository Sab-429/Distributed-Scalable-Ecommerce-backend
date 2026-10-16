package messaging

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"
)

// RabbitMQ owns one AMQP connection + channel. The payment-service opens two
// instances: one for publishing, one for consuming, so a stuck consumer never
// blocks publishes (and vice versa).
type RabbitMQ struct {
	Conn    *amqp.Connection
	Channel *amqp.Channel
}

func NewRabbitMQ(
	host string,
	port string,
	username string,
	password string,
	vhost string,
) (*RabbitMQ, error) {

	// Build the URI with url.URL so credentials are escaped and the vhost is
	// correctly joined: "amqp://u:p@host:5672/vhost" or "amqp://u:p@host:5672/".
	vhostPath := strings.TrimPrefix(vhost, "/")
	uri := url.URL{
		Scheme: "amqp",
		User:   url.UserPassword(username, password),
		Host:   host + ":" + port,
		Path:   "/" + vhostPath,
	}

	conn, err := amqp.Dial(uri.String())
	if err != nil {
		return nil, fmt.Errorf("rabbitmq connection failed: %w", err)
	}

	channel, err := conn.Channel()
	if err != nil {
		conn.Close()

		return nil, fmt.Errorf(
			"rabbitmq channel creation failed: %w",
			err,
		)
	}

	log.Println("RabbitMQ connected")

	return &RabbitMQ{
		Conn:    conn,
		Channel: channel,
	}, nil
}

func (r *RabbitMQ) Close() {

	if r.Channel != nil {
		_ = r.Channel.Close()
	}

	if r.Conn != nil {
		_ = r.Conn.Close()
	}
}
