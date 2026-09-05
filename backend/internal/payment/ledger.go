package payment

import (
	"fmt"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/platform/database"
)

// LedgerEntry is the application projection of a points_ledger row. Integer
// fields are int64; the HTTP layer serializes public int64 values as decimal
// strings per the OpenAPI contract.
type LedgerEntry struct {
	ID           uid.ID
	UserID       uid.ID
	OrderID      *uid.ID
	Type         database.LedgerType
	Delta        int64
	BalanceAfter int64
	Remark       string
	CreatedAt    time.Time
}

// OrderPayEventKey returns the unique event key for an order_pay ledger entry.
// The key is derived from the order id, which is only known after the order
// insert inside the same transaction; the uq_order_pay partial index is the
// backstop that rejects a second payment entry for the same order.
func OrderPayEventKey(orderID uid.ID) string {
	return fmt.Sprintf("order_pay:%s", orderID)
}

// AdminAdjustEventKey returns the unique event key for an administrator points
// adjustment. The Idempotency-Key is part of the key so a retried request with
// the same header maps to the same event and cannot double-apply the delta.
func AdminAdjustEventKey(adminID uid.ID, idempotencyKey string) string {
	return fmt.Sprintf("admin_adjust:%s:%s", adminID, idempotencyKey)
}

// OrderRefundEventKey returns the unique event key for an order_refund ledger
// entry. The key is derived from the order id, which is only known after the
// order insert; the uq_order_refund partial index is the backstop that rejects
// a second refund entry for the same order.
func OrderRefundEventKey(orderID uid.ID) string {
	return fmt.Sprintf("order_refund:%s", orderID)
}

// ToLedgerEntry projects a database ledger row into the application type.
func ToLedgerEntry(row *database.PointsLedger) LedgerEntry {
	if row == nil {
		return LedgerEntry{}
	}
	return LedgerEntry{
		ID:           row.ID,
		UserID:       row.UserID,
		OrderID:      row.OrderID,
		Type:         row.Type,
		Delta:        row.Delta,
		BalanceAfter: row.BalanceAfter,
		Remark:       row.Remark,
		CreatedAt:    row.CreatedAt,
	}
}
