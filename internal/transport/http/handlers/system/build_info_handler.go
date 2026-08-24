package system

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/version"
)

// BuildInfoResponse is the build metadata exposed by the build-info endpoint.
type BuildInfoResponse struct {
	Version   string `json:"version" example:"dev"`
	Commit    string `json:"commit" example:"unknown"`
	BuildDate string `json:"buildDate" example:"unknown"`
}

type BuildInfoHandler struct{}

func NewBuildInfoHandler() *BuildInfoHandler {
	return &BuildInfoHandler{}
}

// BuildInfo godoc
// @Summary Get service build metadata
// @Tags system
// @Produce json
// @Success 200 {object} system.BuildInfoResponse
// @Router /build-info [get]
func (handler *BuildInfoHandler) BuildInfo(c *gin.Context) {
	response.JSON(c, http.StatusOK, BuildInfoResponse{
		Version:   version.Version,
		Commit:    version.Commit,
		BuildDate: version.BuildDate,
	})
}
