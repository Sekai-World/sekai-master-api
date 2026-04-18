package system

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
)

type HealthHandler struct {
	db *sql.DB
}

func NewHealthHandler(db *sql.DB) *HealthHandler {
	return &HealthHandler{db: db}
}

// Check godoc
// @Summary Get health status
// @Tags system
// @Produce json
// @Success 200 {object} shared.HealthResponse
// @Router /health [get]
func (handler *HealthHandler) Check(c *gin.Context) {
	databaseStatus := "up"
	if handler.db != nil {
		if err := handler.db.Ping(); err != nil {
			databaseStatus = "down"
		}
	}

	response.JSON(c, http.StatusOK, gin.H{
		"status":   "ok",
		"database": databaseStatus,
	})
}
