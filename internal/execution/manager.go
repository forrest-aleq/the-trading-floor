package execution

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

// Manager handles order lifecycle
type Manager struct {
	ibkr *ibkr.Client
	log  *slog.Logger
}

func NewManager(ibkrClient *ibkr.Client) *Manager {
	return &Manager{
		ibkr: ibkrClient,
		log:  slog.Default().With("component", "execution"),
	}
}

// Submit places an order through IBKR
func (m *Manager) Submit(ctx context.Context, token *model.CapToken, order model.Order) (*model.Fill, error) {
	// Validate capability token
	if token == nil {
		return nil, fmt.Errorf("missing capability token")
	}

	// Validate connection
	if !m.ibkr.IsConnected() {
		return nil, fmt.Errorf("IBKR not connected")
	}

	m.log.Info("submitting order",
		"thesis_id", order.ThesisID,
		"desk_id", order.DeskID,
		"symbol", order.Instrument.Symbol,
		"direction", order.Direction,
		"quantity", order.Quantity,
		"type", order.OrderType,
		"paper", m.ibkr.IsPaper(),
	)

	// Place via IBKR
	fill, err := m.ibkr.PlaceOrder(ctx, order)
	if err != nil {
		m.log.Error("order failed",
			"thesis_id", order.ThesisID,
			"error", err,
		)
		return nil, fmt.Errorf("place order: %w", err)
	}

	m.log.Info("order filled",
		"thesis_id", order.ThesisID,
		"symbol", fill.Instrument.Symbol,
		"price", fill.AvgPrice,
		"quantity", fill.Quantity,
	)

	return fill, nil
}

// Cancel cancels a pending order
func (m *Manager) Cancel(ctx context.Context, orderID int64) error {
	return m.ibkr.CancelOrder(ctx, orderID)
}

// GetPositions returns current IBKR positions for reconciliation
func (m *Manager) GetPositions(ctx context.Context) ([]ibkr.IBKRPosition, error) {
	return m.ibkr.GetPositions(ctx)
}

// GetAccountSummary returns account balance
func (m *Manager) GetAccountSummary(ctx context.Context) (*ibkr.AccountSummary, error) {
	return m.ibkr.GetAccountSummary(ctx)
}
