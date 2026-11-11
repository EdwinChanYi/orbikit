// Package event publishes chatroom messages to Kafka.
package event

import (
	"github.com/IBM/sarama"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/yourname/roomkit/api/gen/servicepb"
	"github.com/yourname/roomkit/pkg/log"
)

// Topics names the Kafka topics the chatroom service produces to.
type Topics struct {
	Broadcast  string `yaml:"broadcast"`
	FullServer string `yaml:"fullserver"`
	Kicked     string `yaml:"kicked"`
	Event      string `yaml:"event"`
}

// Producer serializes chatroom messages and sends them synchronously.
type Producer struct {
	p      sarama.SyncProducer
	topics Topics
}

// NewProducer wraps p.
func NewProducer(p sarama.SyncProducer, topics Topics) *Producer {
	return &Producer{p: p, topics: topics}
}

func (p *Producer) send(topic string, m proto.Message) error {
	b, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	_, _, err = p.p.SendMessage(&sarama.ProducerMessage{Topic: topic, Value: sarama.ByteEncoder(b)})
	return err
}

// Broadcast sends a room broadcast to the members listed in the message.
func (p *Producer) Broadcast(m *servicepb.ChatroomBcst) {
	if err := p.send(p.topics.Broadcast, m); err != nil {
		log.L().Error("kafka send broadcast message", zap.Error(err))
	}
}

// FullServer sends a broadcast to every online user of the app.
func (p *Producer) FullServer(m *servicepb.ChatroomBcst) {
	if err := p.send(p.topics.FullServer, m); err != nil {
		log.L().Error("kafka send full server message", zap.Error(err))
	}
}

// Kicked notifies the gateway that members were removed from a room.
func (p *Producer) Kicked(m *servicepb.ChatRoomMemberBeKickedBcst) {
	if err := p.send(p.topics.Kicked, m); err != nil {
		log.L().Error("kafka send kicked message", zap.Error(err))
	}
}

// Misc publishes a miscellaneous chatroom event.
func (p *Producer) Misc(m *servicepb.ChatRoomMiscEventMsg) error {
	err := p.send(p.topics.Event, m)
	if err != nil {
		log.L().Error("kafka send misc event message", zap.Error(err))
	}
	return err
}
