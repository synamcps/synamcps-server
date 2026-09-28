package bus

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// KafkaClient is a lightweight placeholder client. When brokers are configured,
// Produce/Consume use an in-process buffer keyed by topic until a full kafka
// driver is wired; the compose profile still provisions Kafka for operators.
type KafkaClient struct {
	brokers []string
	mu      sync.Mutex
	topics  map[string][][]byte
	offs    map[string]map[string]int
}

func NewKafkaClient(brokers []string) *KafkaClient {
	if len(brokers) == 0 {
		return nil
	}
	return &KafkaClient{brokers: brokers, topics: map[string][][]byte{}, offs: map[string]map[string]int{}}
}

func (c *KafkaClient) Produce(ctx context.Context, topic string, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.topics[topic] = append(c.topics[topic], append([]byte{}, payload...))
	return nil
}

func (c *KafkaClient) Consume(ctx context.Context, topic, consumer string, max int) ([][]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.offs[topic] == nil {
		c.offs[topic] = map[string]int{}
	}
	start := c.offs[topic][consumer]
	msgs := c.topics[topic]
	if start >= len(msgs) {
		return nil, nil
	}
	end := start + max
	if end > len(msgs) {
		end = len(msgs)
	}
	out := msgs[start:end]
	c.offs[topic][consumer] = end
	_ = time.Now()
	return out, nil
}

func (c *KafkaClient) String() string {
	return fmt.Sprintf("kafka(%v)", c.brokers)
}
