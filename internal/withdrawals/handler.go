package withdrawals

import (
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Handler struct {
	withdrawalService *WithdrawalService
	logger            *slog.Logger
}

func NewHandler(withdrawalService *WithdrawalService, logger *slog.Logger) *Handler {
	return &Handler{
		withdrawalService: withdrawalService,
		logger:            logger,
	}
}

type CreateWithdrawalRequest struct {
	UserID         uuid.UUID       `json:"user_id" binding:"required,uuid"`
	Amount         decimal.Decimal `json:"amount" binding:"required,gt=0"`
	Currency       string          `json:"currency" binding:"required"`
	Destination    string          `json:"destination" binding:"required"`
	IdempotencyKey string          `json:"idempotency_key" binding:"required"`
}

type WithdrawalResponse struct {
	ID             uuid.UUID       `json:"id"`
	UserID         uuid.UUID       `json:"user_id"`
	Amount         decimal.Decimal `json:"amount"`
	Currency       string          `json:"currency"`
	Destination    string          `json:"destination"`
	Status         string          `json:"status"`
	IdempotencyKey string          `json:"idempotency_key"`
	CreatedAt      string          `json:"created_at"`
}

func AuthMiddleware() gin.HandlerFunc {
	token := os.Getenv("BEARER_TOKEN")
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth != "Bearer "+token {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

func InitRoutes(r *gin.Engine, service *WithdrawalService, logger *slog.Logger) {
	h := NewHandler(service, logger)

	api := r.Group("/v1", AuthMiddleware())
	{
		api.POST("/withdrawals", h.CreateWithdrawal)
		api.GET("/withdrawals/:id", h.GetWithdrawal)
	}
}

func (h *Handler) CreateWithdrawal(c *gin.Context) {
	var req CreateWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request payload",
			"error", err,
			"client_ip", c.ClientIP(),
			"path", c.FullPath(),
		)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request payload"})
		return
	}
	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request payload"})
		return
	}

	w, err := h.withdrawalService.CreateWithdrawal(c, req.UserID, req.Amount, req.Currency, req.Destination, req.IdempotencyKey)
	if err != nil {
		if errors.Is(err, ErrInsufficientBalance) {
			h.logger.Warn("withdrawal rejected: insufficient balance",
				"user_id", req.UserID,
				"idempotency_key", req.IdempotencyKey,
			)
			c.JSON(http.StatusConflict, gin.H{"error": ErrInsufficientBalance.Error()})
			return
		}

		if errors.Is(err, ErrIdempotencyPayloadMismatch) {
			h.logger.Warn("withdrawal rejected: idempotency payload mismatch",
				"user_id", req.UserID,
				"idempotency_key", req.IdempotencyKey,
			)
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": ErrIdempotencyPayloadMismatch.Error()})
			return
		}

		h.logger.Error("withdrawal failed: internal error",
			"error", err,
			"user_id", req.UserID,
			"idempotency_key", req.IdempotencyKey,
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal error",
		})
		return
	}

	c.JSON(http.StatusOK, WithdrawalResponse{
		ID:             w.ID,
		UserID:         w.UserID,
		Amount:         w.Amount,
		Currency:       w.Currency,
		Destination:    w.Destination,
		Status:         w.Status,
		IdempotencyKey: w.IdempotencyKey,
		CreatedAt:      w.CreatedAt.Format("2006-01-02 15:04:05"),
	})
}

func (h *Handler) GetWithdrawal(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "not implemented"})
}
