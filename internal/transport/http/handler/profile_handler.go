package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
)

type ProfileHandler struct{}

func NewProfileHandler() *ProfileHandler {
	return &ProfileHandler{}
}

func (handler *ProfileHandler) Me(c *gin.Context) {
	claims, _ := c.Get("claims")
	response.JSON(c, http.StatusOK, gin.H{
		"user": claims,
	})
}
