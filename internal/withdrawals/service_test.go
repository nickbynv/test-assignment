package withdrawals

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupLogger() *slog.Logger {
	return slog.New(
		slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}),
	)
}

func setupTestDB(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "postgres:15",
			Env: map[string]string{
				"POSTGRES_USER":     "postgres",
				"POSTGRES_PASSWORD": "secret",
				"POSTGRES_DB":       "testdb",
			},
			ExposedPorts: []string{"5432/tcp"},
			WaitingFor:   wait.ForListeningPort("5432/tcp"),
		},
		Started: true,
	})
	require.NoError(t, err)

	host, _ := container.Host(ctx)
	port, _ := container.MappedPort(ctx, "5432")
	dsn := fmt.Sprintf("postgres://postgres:secret@%s:%s/testdb?sslmode=disable", host, port.Port())

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	schema, err := os.ReadFile("../../schema.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(schema))
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Close()
		container.Terminate(ctx)
	})

	return pool
}

func TestCreateWithdrawal_Success(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	defer db.Close()

	service := NewWithdrawalService(db, setupLogger())

	var userID uuid.UUID
	err := db.QueryRow(ctx,
		"INSERT INTO users (balance) VALUES (1000) RETURNING id",
	).Scan(&userID)
	require.NoError(t, err)

	amount := decimal.NewFromFloat(500).Round(8)
	w, err := service.CreateWithdrawal(ctx, userID, amount, "USDT", "dest_abc", "key123")

	require.NoError(t, err)
	require.Equal(t, amount, w.Amount)
	require.Equal(t, "pending", w.Status)
}

func TestCreateWithdrawal_InsufficientBalance(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	defer db.Close()

	service := NewWithdrawalService(db, setupLogger())

	var userID uuid.UUID
	err := db.QueryRow(ctx,
		"INSERT INTO users (balance) VALUES (100) RETURNING id",
	).Scan(&userID)
	require.NoError(t, err)

	amount := decimal.NewFromFloat(200).Round(8)
	_, err = service.CreateWithdrawal(ctx, userID, amount, "USDT", "dest_abc", "key123")

	require.ErrorIs(t, err, ErrInsufficientBalance)
}

func TestCreateWithdrawal_Idempotency(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	defer db.Close()

	service := NewWithdrawalService(db, setupLogger())

	var userID uuid.UUID
	err := db.QueryRow(ctx,
		"INSERT INTO users (balance) VALUES (1000) RETURNING id",
	).Scan(&userID)
	require.NoError(t, err)

	amount := decimal.NewFromFloat(300).Round(8)

	w1, err := service.CreateWithdrawal(ctx, userID, amount, "USDT", "dest", "idem1")
	require.NoError(t, err)

	w2, err := service.CreateWithdrawal(ctx, userID, amount, "USDT", "dest", "idem1")
	require.NoError(t, err)

	require.Equal(t, w1.ID, w2.ID)
}

func TestCreateWithdrawal_Concurrent(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	defer db.Close()

	service := NewWithdrawalService(db, setupLogger())

	var userID uuid.UUID
	err := db.QueryRow(ctx,
		"INSERT INTO users (balance) VALUES (100) RETURNING id",
	).Scan(&userID)
	require.NoError(t, err)

	wg := sync.WaitGroup{}
	successCount := int32(0)

	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := service.CreateWithdrawal(
				ctx,
				userID,
				decimal.NewFromFloat(30).Round(8),
				"USDT",
				"dest",
				fmt.Sprintf("key-%d", i),
			)
			if err == nil {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	require.LessOrEqual(t, successCount, int32(3))
}
