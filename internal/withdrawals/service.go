package withdrawals

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

var (
	ErrInsufficientBalance        = errors.New("insufficient balance")
	ErrIdempotencyPayloadMismatch = errors.New("duplicate idempotency key with different payload")
	ErrUnsupportedCurrency        = errors.New("currency not supported")
	ErrInvalidAmount              = errors.New("amount is invalid")
)

const (
	getAndBlockBalanceQuery = `SELECT balance FROM users WHERE id = $1 FOR UPDATE`

	insertWithdrawalQuery = `
		INSERT INTO withdrawals (
			user_id,
			amount,
			currency,
			destination,
			idempotency_key
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
		RETURNING
			id,
			user_id,
			amount,
			currency,
			destination,
			status,
			idempotency_key,
			created_at;
	`

	getWithdrawalByIdempotencyKeyQuery = `
		SELECT id, user_id, amount, currency, destination, status, idempotency_key, created_at
		FROM withdrawals
		WHERE user_id = $1 AND idempotency_key = $2
	`

	updateBalanceQuery = `UPDATE users SET balance = balance - $1 WHERE id = $2`
)

type Withdrawal struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Amount         decimal.Decimal
	Currency       string
	Destination    string
	Status         string
	IdempotencyKey string
	CreatedAt      time.Time
}

var SupportedCurrencies = map[string]struct{}{
	"USDT": {},
}

type WithdrawalService struct {
	db     *pgxpool.Pool
	logger *slog.Logger
}

func NewWithdrawalService(db *pgxpool.Pool, logger *slog.Logger) *WithdrawalService {
	return &WithdrawalService{
		db:     db,
		logger: logger,
	}
}

func (s *WithdrawalService) CreateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency, destination, idempotencyKey string) (*Withdrawal, error) {
	s.logger.Info("withdrawal create attempt",
		"user_id", userID,
		"amount", amount,
		"currency", currency,
		"destination", destination,
		"idempotency_key", idempotencyKey,
	)

	// 1. validation
	if _, found := SupportedCurrencies[currency]; !found {
		s.logger.Warn("unsupported currency",
			"user_id", userID,
			"currency", currency,
			"idempotency_key", idempotencyKey,
		)
		return nil, ErrUnsupportedCurrency
	}
	if !amount.GreaterThan(decimal.Zero) {
		s.logger.Warn("invalid withdrawal amount",
			"user_id", userID,
			"amount", amount,
			"idempotency_key", idempotencyKey,
		)
		return nil, ErrInvalidAmount
	}

	// 2. tx start
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadWrite,
	})
	if err != nil {
		s.logger.Error("failed to start transaction",
			"error", err,
			"user_id", userID,
			"idempotency_key", idempotencyKey,
		)
		return nil, err
	}
	defer func() {
		if rErr := tx.Rollback(ctx); rErr != nil && !errors.Is(rErr, pgx.ErrTxClosed) {
			s.logger.Error("rollback failed",
				"error", rErr,
				"user_id", userID,
				"idempotency_key", idempotencyKey,
			)
		}
	}()

	// 3. balance blocking & checking
	var balance decimal.Decimal
	err = tx.QueryRow(ctx, getAndBlockBalanceQuery, userID).Scan(&balance)
	if err != nil {
		s.logger.Error("failed to read user balance",
			"error", err,
			"user_id", userID,
		)
		return nil, err
	}
	if balance.LessThan(amount) {
		s.logger.Warn("insufficient balance",
			"user_id", userID,
			"balance", balance,
			"requested", amount,
			"idempotency_key", idempotencyKey,
		)
		return nil, ErrInsufficientBalance
	}

	// 4. debit balance
	_, err = tx.Exec(ctx, updateBalanceQuery, amount, userID)
	if err != nil {
		s.logger.Error("failed to debit user balance",
			"error", err,
			"user_id", userID,
			"amount", amount,
			"idempotency_key", idempotencyKey,
		)
		return nil, err
	}

	// 5. creating withdrawal
	var w Withdrawal
	s.logger.Info("attempting to insert withdrawal",
		"user_id", userID,
		"amount", amount,
		"currency", currency,
		"destination", destination,
		"idempotency_key", idempotencyKey,
	)
	err = tx.QueryRow(ctx, insertWithdrawalQuery,
		userID,
		amount,
		currency,
		destination,
		idempotencyKey,
	).Scan(
		&w.ID,
		&w.UserID,
		&w.Amount,
		&w.Currency,
		&w.Destination,
		&w.Status,
		&w.IdempotencyKey,
		&w.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		s.logger.Info("insert returned no rows, checking existing withdrawal by idempotency key",
			"idempotency_key", idempotencyKey,
		)
		err := s.db.QueryRow(ctx, getWithdrawalByIdempotencyKeyQuery, userID, idempotencyKey).Scan(
			&w.ID,
			&w.UserID,
			&w.Amount,
			&w.Currency,
			&w.Destination,
			&w.Status,
			&w.IdempotencyKey,
			&w.CreatedAt,
		)
		if err != nil {
			s.logger.Error("failed to fetch existing withdrawal",
				"idempotency_key", idempotencyKey,
				"error", err,
			)
			return nil, err
		}
		if !(w.UserID == userID && w.Amount.Equal(amount) && w.Currency == currency && w.Destination == destination) {
			s.logger.Warn("idempotency payload mismatch",
				"user_id", userID,
				"idempotency_key", idempotencyKey,
			)
			return nil, ErrIdempotencyPayloadMismatch
		}
		s.logger.Info("returning existing withdrawal due to idempotency",
			"withdrawal_id", w.ID,
			"user_id", w.UserID,
		)
		return &w, nil
	}
	if err != nil {
		s.logger.Error("failed to insert withdrawal",
			"user_id", userID,
			"idempotency_key", idempotencyKey,
			"error", err,
		)
		return nil, err
	}
	s.logger.Info("withdrawal inserted successfully",
		"withdrawal_id", w.ID,
		"user_id", w.UserID,
	)

	// 6. commit
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("transaction commit failed",
			"error", err,
			"user_id", userID,
			"idempotency_key", idempotencyKey,
		)
		return nil, err
	}

	return &w, nil
}
