package queue

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

type RabbitClient struct {
	url  string
	conn *amqp.Connection
}

func NewRabbitClient(url string) *RabbitClient {
	if url == "" {
		return nil
	}
	return &RabbitClient{url: url}
}

func (c *RabbitClient) connect() (*amqp.Connection, error) {
	if c.conn != nil && !c.conn.IsClosed() {
		return c.conn, nil
	}
	conn, err := amqp.Dial(c.url)
	if err != nil {
		return nil, err
	}
	c.conn = conn
	return conn, nil
}

func (c *RabbitClient) Enqueue(ctx context.Context, queueName string, payload []byte) error {
	conn, err := c.connect()
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		return err
	}
	return ch.PublishWithContext(ctx, "", queueName, false, false, amqp.Publishing{ContentType: "application/json", Body: payload})
}

func (c *RabbitClient) Dequeue(ctx context.Context, queueName string) ([]byte, bool, error) {
	conn, err := c.connect()
	if err != nil {
		return nil, false, err
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, false, err
	}
	defer ch.Close()
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		return nil, false, err
	}
	msg, ok, err := ch.Get(queueName, true)
	if err != nil || !ok {
		return nil, ok, err
	}
	return msg.Body, true, nil
}

func (c *RabbitClient) Peek(ctx context.Context, queueName string) ([]byte, bool, error) {
	// AMQP has no true peek without ack; approximate via Get+Nack requeue.
	conn, err := c.connect()
	if err != nil {
		return nil, false, err
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, false, err
	}
	defer ch.Close()
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		return nil, false, err
	}
	msg, ok, err := ch.Get(queueName, false)
	if err != nil || !ok {
		return nil, ok, err
	}
	_ = ch.Nack(msg.DeliveryTag, false, true)
	return msg.Body, true, nil
}

func (c *RabbitClient) Size(ctx context.Context, queueName string) (int64, error) {
	conn, err := c.connect()
	if err != nil {
		return 0, err
	}
	ch, err := conn.Channel()
	if err != nil {
		return 0, err
	}
	defer ch.Close()
	q, err := ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		return 0, err
	}
	return int64(q.Messages), nil
}

func (c *RabbitClient) String() string {
	return fmt.Sprintf("rabbitmq(%s)", c.url)
}
