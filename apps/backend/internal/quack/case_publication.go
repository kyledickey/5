package quack

import (
	"context"
	"errors"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// casePublicationWriter is the optional durable receipt capability. Case
// persistence adapters need not implement Discord publication storage to serve
// other case use cases; missing support retains the adapter's bounded fallback.
type casePublicationWriter interface {
	SaveCasePublication(context.Context, model.CasePublication) error
}

// RecordPublicReceipt registers a message already delivered for a committed case.
// It is an adapter delivery use case, not moderator authorization or enforcement:
// the caller must have obtained the case through creation/authorized access and
// supply only its public presentation. The idempotent store preserves the first
// snapshot. Three immediate attempts retain the existing registration behavior;
// failure leaves the caller responsible for private feedback and bounded refresh.
func (s *CaseService) RecordPublicReceipt(ctx context.Context, receipt model.CasePublication) error {
	writer, ok := s.store.(casePublicationWriter)
	if !ok {
		return errors.New("durable case publication storage unavailable")
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = writer.SaveCasePublication(ctx, receipt)
		if err == nil || ctx.Err() != nil {
			break
		}
	}
	return err
}

// PublicReceiptActionStatuses returns only IDs and public execution status for
// an already committed case receipt. It performs no action, audit write or live
// moderator check: publication recovery continues after the initiating staff
// interaction ends, using the previously authorized public case projection.
func (s *CaseService) PublicReceiptActionStatuses(ctx context.Context, caseID string) ([]CaseActionResponse, error) {
	actions, err := s.store.ListCaseActionExecutions(ctx, caseID)
	if err != nil {
		return nil, err
	}
	result := make([]CaseActionResponse, 0, len(actions))
	for _, action := range actions {
		result = append(result, CaseActionResponse{ID: action.ID, Status: action.Status})
	}
	return result, nil
}
