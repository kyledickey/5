package store

import (
	"errors"
	"sync"

	"gorm.io/gorm"
)

// ErrAuditImmutable rejects application-level mutation or deletion of permanent audit history.
var ErrAuditImmutable = errors.New("audit entries are append-only")

// auditCallbackMu serializes callback registration because GORM's callback
// registry is not safe for concurrent Get/Register from several New calls.
var auditCallbackMu sync.Mutex

// installAuditImmutability registers rejectAuditMutation before GORM's update
// and delete callbacks. Registration is idempotent per *gorm.DB; transactions
// share their parent's callback registry, so wrapping a tx re-checks and skips.
func installAuditImmutability(db *gorm.DB) {
	if db == nil {
		return
	}
	auditCallbackMu.Lock()
	defer auditCallbackMu.Unlock()
	if db.Callback().Update().Get("quack:audit_append_only") == nil {
		_ = db.Callback().Update().Before("gorm:update").Register("quack:audit_append_only", rejectAuditMutation) // Register only fails for a duplicate name, which Get just excluded
	}
	if db.Callback().Delete().Get("quack:audit_append_only") == nil {
		_ = db.Callback().Delete().Before("gorm:delete").Register("quack:audit_append_only", rejectAuditMutation) // same: name was just checked
	}
}

// rejectAuditMutation aborts any UPDATE or DELETE statement whose target table
// is audit_log_entries with ErrAuditImmutable, regardless of which model or
// Table() call produced it.
func rejectAuditMutation(db *gorm.DB) {
	if db != nil && db.Statement != nil && db.Statement.Table == "audit_log_entries" {
		db.AddError(ErrAuditImmutable)
	}
}
