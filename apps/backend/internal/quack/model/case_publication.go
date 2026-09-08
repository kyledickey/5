package model

import "time"

// CasePublication tracks a public case message independently of an expiring
// interaction token. PresentationJSON contains only the original public display
// fields; it is never a serialized staff case detail or evidence record.
type CasePublication struct {
	MessageID        string    `gorm:"size:32;primaryKey"`
	CaseID           string    `gorm:"type:char(26);not null;index"`
	ChannelID        string    `gorm:"size:32;not null"`
	PresentationJSON string    `gorm:"type:longtext;not null"`
	LastDigest       string    `gorm:"size:64;not null"`
	RetryAt          time.Time `gorm:"not null;index;index:idx_case_publication_due,priority:2"`
	// RefreshRequested limits scanning to new, changed or still-pending receipts.
	RefreshRequested bool `gorm:"not null;default:true;index:idx_case_publication_due,priority:1"`
	// Revision fences completion against mutations committed during a refresh.
	Revision uint64 `gorm:"not null;default:0"`
}

// TableName keeps transport receipts separate from moderation history.
func (CasePublication) TableName() string { return "case_publications" }
