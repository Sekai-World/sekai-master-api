package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/usecase"
)

func TestLeaseWithoutCoordinationReturnsNullLease(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name           string
		masterDataSync *usecase.MasterDataSyncUsecase
	}{
		{"sync not configured", nil},
		{"lease coordinator disabled", &usecase.MasterDataSyncUsecase{}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := NewMasterDataAdminHandler(testCase.masterDataSync, nil)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/master-data/lease", nil)

			handler.Lease(ctx)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", recorder.Code)
			}
			if !strings.Contains(recorder.Body.String(), `"lease":null`) {
				t.Fatalf("body = %s, want lease serialized as null", recorder.Body.String())
			}
		})
	}
}
