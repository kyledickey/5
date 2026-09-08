package tickets

import "context"

// ClosurePending reports whether a resolved ticket still holds its owner's slot.
// The current ticket authorizes the caller before inspecting the owner's reservation; only a
// successful transcript publication and Discord cleanup release this reservation.
func (s *Service) ClosurePending(ctx context.Context, actor Actor, ticketID string) (bool, error) {
	ticket, err := s.authorizedTicket(ctx, actor, ticketID)
	if err != nil {
		return false, err
	}
	if ticket.Status != StatusResolved {
		return false, nil
	}
	active, err := s.store.activeForMember(ctx, ticket.GuildID, ticket.OwnerDiscordUserID)
	return active != nil && active.ID == ticket.ID, err
}
