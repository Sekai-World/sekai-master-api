package lookups

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type characterBatchTestResponse struct {
	Items      []map[string]any `json:"items"`
	MissingIDs []int64          `json:"missingIds"`
}

func serveCharacterBatchRequest(t *testing.T, route string, handler gin.HandlerFunc, target string) characterBatchTestResponse {
	t.Helper()

	recorder := requestCharacterBatch(t, route, handler, target)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var body characterBatchTestResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func requestCharacterBatch(t *testing.T, route string, handler gin.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET(route, handler)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertCharacterBatchItems(t *testing.T, body characterBatchTestResponse, itemIDs []int64, gameCharacterIDs []int64, missingIDs []int64) {
	t.Helper()

	if len(body.Items) != len(itemIDs) || len(itemIDs) != len(gameCharacterIDs) {
		t.Fatalf("unexpected items: %#v", body.Items)
	}
	for index, item := range body.Items {
		if item["id"] != float64(itemIDs[index]) || item["gameCharacterId"] != float64(gameCharacterIDs[index]) {
			t.Fatalf("unexpected item %d: %#v", index, item)
		}
	}
	if len(body.MissingIDs) != len(missingIDs) {
		t.Fatalf("unexpected missing ids: %#v", body.MissingIDs)
	}
	for index, id := range body.MissingIDs {
		if id != missingIDs[index] {
			t.Fatalf("unexpected missing id %d: %#v", index, body.MissingIDs)
		}
	}
}

func assertCharacterBatchRejectsInvalidIDs(t *testing.T, route string, handler gin.HandlerFunc, targetPrefix string) {
	t.Helper()

	for _, query := range []string{"", "0", "bad", "1,,2"} {
		recorder := requestCharacterBatch(t, route, handler, targetPrefix+"?ids="+query)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("query %q: expected 400, got %d", query, recorder.Code)
		}
	}
}
