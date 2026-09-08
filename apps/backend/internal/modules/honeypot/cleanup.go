package honeypot

import (
	"context"
	"errors"
	"time"
)

// MessageCleaner deletes an already-qualified trigger without invoking moderation.
// Unknown messages are successful cleanup, making lease recovery safe to replay.
type MessageCleaner interface {
	DeleteHoneypotMessage(context.Context, string, string) error
}

// ProcessCleanups drains a bounded batch of durable cleanup. The case applier's
// optional transport capability keeps deletion separate from case execution;
// polling retries failures and recovers interrupted attempts after lease expiry.
func (s *Service) ProcessCleanups(ctx context.Context, limit int) error {
	if s == nil || s.store == nil {
		return errors.New("honeypot cleanup is not configured")
	}
	cleaner, ok := s.applier.(MessageCleaner)
	if !ok {
		return nil
	}
	var failures []error
	for range min(max(limit, 1), 25) {
		// Lease only the message about to be processed so slow Discord calls cannot
		// consume the leases of later messages in a batch.
		messages, err := s.store.claimCleanups(ctx, 1)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		if len(messages) == 0 {
			break
		}
		message := messages[0]
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		deleteErr := cleaner.DeleteHoneypotMessage(attemptCtx, message.ChannelDiscordID, message.MessageDiscordID)
		cancel()
		if err := s.store.finishCleanup(ctx, message, deleteErr == nil); err != nil {
			failures = append(failures, err)
		}
		if deleteErr != nil {
			failures = append(failures, deleteErr)
		}
	}
	return errors.Join(failures...)
}
