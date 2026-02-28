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

	response.JSON(c, http.StatusOK, gin.H{
		"user": gin.H{
			"id":           stringClaim(claims, "sub"),
			"username":     username,
			"display_name": displayName,
			"email":        stringClaim(claims, "email"),
		},
	})
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
