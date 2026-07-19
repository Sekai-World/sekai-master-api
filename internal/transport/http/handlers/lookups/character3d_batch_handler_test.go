package lookups

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCharacter3DsBatchReturnsOrderedMappingsAndMissingIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{"jp": {"character3ds": {
			"1180": {"id": 1180, "characterId": 13, "unit": "theme_park", "name": "Tsukasa"},
			"1197": {"id": 1197, "characterId": 26, "unit": "piapro", "name": "KAITO"},
		}}},
		hasRecords: map[string]map[string]bool{"jp": {"character3ds": true}},
	}
	router := gin.New()
	router.GET("/character3ds/:region/batch", newReadyLookupHandler(cache).Character3DsBatch)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/character3ds/jp/batch?ids=1197,1180,1197,9999", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Items []struct {
			ID              int64 `json:"id"`
			GameCharacterID int64 `json:"gameCharacterId"`
		} `json:"items"`
		MissingIDs []int64 `json:"missingIds"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 || body.Items[0].ID != 1197 || body.Items[0].GameCharacterID != 26 || body.Items[1].ID != 1180 || body.Items[1].GameCharacterID != 13 {
		t.Fatalf("unexpected items: %#v", body.Items)
	}
	if len(body.MissingIDs) != 1 || body.MissingIDs[0] != 9999 {
		t.Fatalf("unexpected missing ids: %#v", body.MissingIDs)
	}
}

func TestCharacter3DsBatchRejectsInvalidIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newReadyLookupHandler(&fakeLookupCache{})
	for _, query := range []string{"", "0", "bad", "1,,2"} {
		router := gin.New()
		router.GET("/character3ds/:region/batch", handler.Character3DsBatch)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/character3ds/jp/batch?ids="+query, nil)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("query %q: expected 400, got %d", query, recorder.Code)
		}
	}
}
