package shared

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
)

type ListSortOptions struct {
	Field      string
	Descending bool
	Enabled    bool
}

func ParseListSortOptions(c *gin.Context) (ListSortOptions, bool) {
	sortBy := strings.TrimSpace(c.Query("sort_by"))
	sortOrder := strings.ToLower(strings.TrimSpace(c.Query("sort_order")))
	if sortBy == "" && sortOrder == "" {
		return ListSortOptions{}, true
	}
	if sortBy == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "sort_by is required when sort_order is provided")
		return ListSortOptions{}, false
	}

	options := ListSortOptions{
		Field:   sortBy,
		Enabled: true,
	}

	switch sortOrder {
	case "", "asc":
		return options, true
	case "desc":
		options.Descending = true
		return options, true
	default:
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "sort_order must be one of: asc, desc")
		return ListSortOptions{}, false
	}
}

func ValidateSortField(c *gin.Context, sortBy string, _ []map[string]any, baseFields []string) bool {
	allowed := sortableFieldSet(baseFields)
	if _, ok := allowed[sortBy]; ok {
		return true
	}

	fields := make([]string, 0, len(allowed))
	for field := range allowed {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	message := "invalid sort_by"
	if len(fields) > 0 {
		message = fmt.Sprintf("sort_by must be one of: %s", strings.Join(fields, ", "))
	}
	response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", message)
	return false
}

func sortableFieldSet(baseFields []string) map[string]struct{} {
	fields := make(map[string]struct{}, len(baseFields))
	for _, field := range baseFields {
		normalized := strings.TrimSpace(field)
		if normalized == "" {
			continue
		}
		fields[normalized] = struct{}{}
	}

	return fields
}

type jsonNumber interface {
	String() string
}

func SortResponseItems(items []map[string]any, sortBy string, descending bool) {
	sort.SliceStable(items, func(i int, j int) bool {
		left := items[i][sortBy]
		right := items[j][sortBy]

		leftNil := left == nil
		rightNil := right == nil
		if leftNil != rightNil {
			return !leftNil
		}

		comparison := compareSortableValues(left, right)
		if comparison == 0 {
			return compareIDValue(items[i]["id"], items[j]["id"]) < 0
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
}

func compareSortableValues(left any, right any) int {
	if leftNumber, ok := sortableNumericValue(left); ok {
		if rightNumber, ok := sortableNumericValue(right); ok {
			switch {
			case leftNumber < rightNumber:
				return -1
			case leftNumber > rightNumber:
				return 1
			default:
				return 0
			}
		}
	}

	leftBool, leftBoolOK := left.(bool)
	rightBool, rightBoolOK := right.(bool)
	if leftBoolOK && rightBoolOK {
		switch {
		case leftBool == rightBool:
			return 0
		case !leftBool && rightBool:
			return -1
		default:
			return 1
		}
	}

	leftText := NormalizeComparableText(left)
	rightText := NormalizeComparableText(right)
	switch {
	case leftText < rightText:
		return -1
	case leftText > rightText:
		return 1
	default:
		return 0
	}
}

func sortableNumericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		if typed > math.MaxInt64 {
			return 0, false
		}
		return float64(typed), true
	case float32:
		return float64(typed), true
	case float64:
		return typed, true
	case jsonNumber:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func compareIDValue(left any, right any) int {
	leftID := NormalizeAnyID(left)
	rightID := NormalizeAnyID(right)
	switch {
	case leftID < rightID:
		return -1
	case leftID > rightID:
		return 1
	default:
		return 0
	}
}

func PaginateItems(items []map[string]any, page int, pageSize int) ([]map[string]any, gin.H) {
	total := len(items)
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}

	start := (page - 1) * pageSize
	if start >= total {
		return []map[string]any{}, gin.H{
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    false,
		}
	}

	end := start + pageSize
	if end > total {
		end = total
	}

	return items[start:end], gin.H{
		"page":        page,
		"page_size":   pageSize,
		"total":       total,
		"total_pages": totalPages,
		"has_next":    page < totalPages,
	}
}
