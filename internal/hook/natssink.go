package hook

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
)

// natsSink publishes events to a NATS subject.
type natsSink struct {
	conn    *nats.Conn
	subject string
}

// NewNATSSink creates a new NATS sink.
func NewNATSSink(target *NATSTarget) (Sink, error) {
	if target == nil || target.URL == "" {
		return nil, fmt.Errorf("nats target url is required")
	}
	if target.Subject == "" {
		return nil, fmt.Errorf("nats target subject is required")
	}

	var opts []nats.Option
	opts = append(opts, nats.Name(UserAgent()))

	if target.Token != "" {
		opts = append(opts, nats.Token(target.Token))
	}
	if target.Username != "" {
		opts = append(opts, nats.UserInfo(target.Username, target.Password))
	}

	conn, err := nats.Connect(target.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats: connecting to %s: %w", target.URL, err)
	}

	return &natsSink{
		conn:    conn,
		subject: target.Subject,
	}, nil
}

// Send publishes the payload to the NATS subject.
func (s *natsSink) Send(_ context.Context, payload []byte, _ string) error {
	if err := s.conn.Publish(s.subject, payload); err != nil {
		return fmt.Errorf("nats: publishing to subject %s: %w", s.subject, err)
	}
	return nil
}

// Close drains and closes the NATS connection.
func (s *natsSink) Close() error {
	s.conn.Close()
	return nil
}
