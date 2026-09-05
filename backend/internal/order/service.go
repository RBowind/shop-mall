package order

import (
	"context"
	"errors"

	"shop-mall/backend/internal/payment"
	"shop-mall/backend/internal/platform/uid"
)

// OrderService is the order read surface. The write paths (checkout, refund,
// fulfillment, points adjustment) are cross-domain usecases injected into the
// handler directly; this service only owns the orders table reads and the
// row-to-projection conversion through the domain Repository.
type OrderService struct {
	repo   *Repository
	ledger *payment.Repository
}

// OrderServiceDeps is the dependency bundle for NewOrderService.
type OrderServiceDeps struct {
	Repo   *Repository
	Ledger *payment.Repository
}

// NewOrderService validates the dependency bundle and returns the service.
func NewOrderService(deps OrderServiceDeps) (*OrderService, error) {
	if deps.Repo == nil {
		return nil, errors.New("order service: repository is required")
	}
	if deps.Ledger == nil {
		return nil, errors.New("order service: ledger repository is required")
	}
	return &OrderService{repo: deps.Repo, ledger: deps.Ledger}, nil
}

// List returns the buyer-owned orders.
func (s *OrderService) List(ctx context.Context, userID uid.ID, page, pageSize int, status *Status) ([]Order, int64, error) {
	rows, total, err := s.repo.ListByUser(ctx, userID, page, pageSize, status)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Order, 0, len(rows))
	for i := range rows {
		out = append(out, ToOrder(&rows[i]))
	}
	return out, total, nil
}

// Get returns a buyer-owned order or ErrOrderNotFound when the order is
// missing or owned by another buyer.
func (s *OrderService) Get(ctx context.Context, userID, orderID uid.ID) (Order, error) {
	row, err := s.repo.GetByUser(ctx, userID, orderID)
	if err != nil {
		return Order{}, err
	}
	return ToOrder(row), nil
}

// ListAll returns every order for the administrator surface.
func (s *OrderService) ListAll(ctx context.Context, page, pageSize int, status *Status) ([]Order, int64, error) {
	rows, total, err := s.repo.ListAll(ctx, page, pageSize, status)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Order, 0, len(rows))
	for i := range rows {
		out = append(out, ToOrder(&rows[i]))
	}
	return out, total, nil
}

// ListRefunds returns the refund-related orders (refund_requested and
// refunded) for the administrator refund surface.
func (s *OrderService) ListRefunds(ctx context.Context, page, pageSize int) ([]Order, int64, error) {
	rows, total, err := s.repo.ListRefunds(ctx, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Order, 0, len(rows))
	for i := range rows {
		out = append(out, ToOrder(&rows[i]))
	}
	return out, total, nil
}

// GetAdmin returns any order regardless of buyer for the administrator surface.
func (s *OrderService) GetAdmin(ctx context.Context, orderID uid.ID) (Order, error) {
	row, err := s.repo.GetByID(ctx, orderID)
	if err != nil {
		return Order{}, err
	}
	return ToOrder(row), nil
}

// ListLedger returns the buyer's points ledger page. The ledger is a payment
// domain projection; the read crosses the domain boundary through the payment
// repository's public API.
func (s *OrderService) ListLedger(ctx context.Context, userID uid.ID, page, pageSize int) ([]payment.LedgerEntry, int64, error) {
	rows, total, err := s.ledger.ListByUser(s.repo.db, ctx, userID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	out := make([]payment.LedgerEntry, 0, len(rows))
	for i := range rows {
		out = append(out, payment.ToLedgerEntry(&rows[i]))
	}
	return out, total, nil
}
