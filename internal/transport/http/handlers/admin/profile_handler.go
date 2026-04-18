package admin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/transport/http/response"
)

type ProfileHandler struct {
	showAuthDebug        bool
	adminClaimAuthorizer *auth.AdminClaimAuthorizer
}

func NewProfileHandler(appEnv string, adminClaimAuthorizer *auth.AdminClaimAuthorizer) *ProfileHandler {
	return &ProfileHandler{
		showAuthDebug:        shouldShowProfileAuthDebug(appEnv),
		adminClaimAuthorizer: adminClaimAuthorizer,
	}
}

// Me godoc
// @Summary Get admin profile
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} shared.ProfileResponse
// @Failure 401 {object} shared.ErrorResponse
// @Failure 403 {object} shared.ErrorResponse
// @Router /admin/profile [get]
func (handler *ProfileHandler) Me(c *gin.Context) {
	rawClaims, _ := c.Get("claims")
	claims, _ := rawClaims.(map[string]any)

	username := firstNonEmpty(
		stringClaim(claims, "preferred_username"),
		stringClaim(claims, "username"),
	)

	displayName := firstNonEmpty(
		stringClaim(claims, "name"),
		buildName(stringClaim(claims, "given_name"), stringClaim(claims, "family_name")),
		username,
	)

	payload := gin.H{
		"user": gin.H{
			"id":           stringClaim(claims, "sub"),
			"username":     username,
			"display_name": displayName,
			"email":        stringClaim(claims, "email"),
		},
	}

	if handler != nil && handler.showAuthDebug && handler.adminClaimAuthorizer != nil {
		if debug := handler.adminClaimAuthorizer.Debug(claims); debug != nil {
			payload["auth_debug"] = gin.H{
				"admin_claim":    debug.ClaimPath,
				"claim_values":   debug.ClaimValues,
				"matched_values": debug.MatchedValues,
			}
		}
	}

	response.JSON(c, http.StatusOK, payload)
}

func stringClaim(claims map[string]any, key string) string {
	if claims == nil {
		return ""
	}

	value, ok := claims[key]
	if !ok {
		return ""
	}

	stringValue, ok := value.(string)
	if !ok {
		return ""
	}

	return stringValue
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}

func buildName(givenName string, familyName string) string {
	fullName := firstNonEmpty(givenName)
	if familyName == "" {
		return fullName
	}

	if fullName == "" {
		return familyName
	}

	return fullName + " " + familyName
}

func shouldShowProfileAuthDebug(appEnv string) bool {
	normalized := strings.ToLower(strings.TrimSpace(appEnv))
	return normalized == "development" || normalized == "dev" || normalized == "test"
}
