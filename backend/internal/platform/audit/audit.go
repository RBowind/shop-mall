// Package audit is the append-only audit_logs write facility shared by the
// admin domain and every usecase that must record an action (product writes,
// checkout fulfillment, refunds, points adjustments). The trigger installed by
// the migration rejects UPDATE and DELETE on the table, so this package
// exposes only the append path.
package audit

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/platform/uid"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Entry is the structured audit row: who acted (actor), on what (target),
// with what outcome and the before/after payloads.
type Entry struct {
	ActorAdminID *uid.ID
	ActorRole    string
	Action       string
	TargetType   string
	TargetID     *uid.ID
	Result       string
	BeforeData   map[string]any
	AfterData    map[string]any
}

// Write inserts an audit_logs row inside the caller's transaction. The write
// runs on the supplied tx so the audit row commits or rolls back together
// with the business change it records.
func Write(tx *gorm.DB, entry Entry) error {
	beforeJSON, err := marshal(entry.BeforeData)
	if err != nil {
		return err
	}
	afterJSON, err := marshal(entry.AfterData)
	if err != nil {
		return err
	}
	row := database.AuditLog{
		ActorAdminID: entry.ActorAdminID,
		ActorRole:    entry.ActorRole,
		Action:       entry.Action,
		TargetType:   entry.TargetType,
		TargetID:     entry.TargetID,
		Result:       entry.Result,
		BeforeData:   beforeJSON,
		AfterData:    afterJSON,
		TraceID:      newTraceID(),
	}
	return tx.WithContext(tx.Statement.Context).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

func marshal(value map[string]any) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	return json.Marshal(value)
}

func newTraceID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return uuid.NewString()
}
