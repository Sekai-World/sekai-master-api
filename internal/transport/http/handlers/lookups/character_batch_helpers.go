package lookups

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
)

func parseCharacterBatchRequest(c *gin.Context, limit int, requiredMessage string) (string, []int64, bool) {
	region := strings.TrimSpace(c.Param("region"))
	parts := strings.Split(c.Query("ids"), ",")
	if region == "" || len(parts) == 0 || len(parts) > limit {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", requiredMessage)
		return "", nil, false
	}

	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "ids must contain positive integers")
			return "", nil, false
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	return region, ids, true
}
