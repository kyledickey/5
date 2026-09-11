package idutil

import (
	"github.com/oklog/ulid/v2"
)

// NewULID returns a new 26-character, lexicographically sortable ULID. Every
// durable record and trace identifier in Quack uses this format so IDs sort by
// creation time. The error return is kept for callers that treat ID generation
// as fallible; the current implementation never fails.
func NewULID() (string, error) {
	return ulid.Make().String(), nil
}
