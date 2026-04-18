package shared

import (
	"fmt"
	"strings"
)

func NormalizeAnyID(value any) string {
	if value == nil {
		return ""
	}

	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func NormalizeComparableText(value any) string {
	if value == nil {
		return ""
	}

	return strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", value)))
}
