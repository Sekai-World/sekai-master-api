package handler

import (
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type MasterDataEventHandler struct {
	hub *usecase.MasterDataEventHub
}

func NewMasterDataEventHandler(hub *usecase.MasterDataEventHub) *MasterDataEventHandler {
	return &MasterDataEventHandler{hub: hub}
}

func (handler *MasterDataEventHandler) Stream(c *gin.Context) {
	if handler.hub == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_EVENTS_DISABLED", "master data events are not enabled")
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	events, unsubscribe := handler.hub.Subscribe()
	defer unsubscribe()

	keepAliveTicker := time.NewTicker(20 * time.Second)
	defer keepAliveTicker.Stop()

	c.Stream(func(writer io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case <-keepAliveTicker.C:
			_, _ = c.Writer.Write([]byte(": keep-alive\n\n"))
			c.Writer.Flush()
			return true
		case event, ok := <-events:
			if !ok {
				return false
			}
			c.SSEvent(event.Event, event)
			return true
		}
	})
}
