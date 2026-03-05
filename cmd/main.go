package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nickbynv/test-assignment/internal/withdrawals"
)

func main() {
	// 1. init logger
	log := setupLogger()
	log.Info("logger initialized")

	// 2. init db
	dsn := os.Getenv("DB_URL")
	if dsn == "" {
		log.Error("DB_URL environment variable is not set")

	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Error("failed to parse DB config",
			"error", err,
			"dsn", dsn,
		)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		log.Error("failed to create DB pool",
			"error", err,
			"dsn", dsn,
		)
	}
	defer pool.Close()
	log.Info("database pool initialized")

	// 3. init service
	service := withdrawals.NewWithdrawalService(pool, log)
	log.Info("withdrawal service initialized")

	// 4. init server
	r := gin.Default()
	r.Use(func(c *gin.Context) {
		log.Info("incoming request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"client_ip", c.ClientIP(),
		)
		c.Next()
	})
	withdrawals.InitRoutes(r, service, log)
	log.Info("routes initialized")

	// 5. starting server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Warn("PORT not set, using default", "port", port)
	}

	log.Info("starting server", "port", port)
	if err := r.Run(":" + port); err != nil {
		log.Error("failed to run server",
			"error", err,
			"port", port,
		)
	}
}

func setupLogger() *slog.Logger {
	return slog.New(
		slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}),
	)
}
