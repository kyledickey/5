package store

import (
	"context"
	"fmt"
	"time"

	r "github.com/redis/go-redis/v9"
)

// OpenRedis parses rawURL, opens a client, and pings it with a five-second
// timeout so startup fails before serving traffic when Redis is unreachable.
func OpenRedis(rawURL string) (*r.Client, error) {
	options, err := r.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := r.NewClient(options)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Ping(ctx).Result(); err != nil {
		_ = client.Close() // best-effort: the ping error is what the caller needs
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}
