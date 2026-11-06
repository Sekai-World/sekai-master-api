package lookups

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
)

func newComicsRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	textTip := map[string]any{"id": 1, "title": "A tip", "description": "Text only", "fromUserRank": 1, "toUserRank": 10}
	blankImageTip := map[string]any{"id": 2, "title": "Blank", "assetbundleName": "  "}
	first := map[string]any{"id": 1001, "title": "First", "assetbundleName": "comic_0001", "fromUserRank": 1, "toUserRank": 10, "unknownField": "must not be exposed"}
	second := map[string]any{"id": 1002, "title": "Second", "assetbundleName": "comic_0002"}
	third := map[string]any{"id": 1003, "title": "Third", "assetbundleName": "comic_0003"}
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {comicsEntity: {"1": textTip, "2": blankImageTip, "1001": first, "1002": second}},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {comicsEntity: {third, textTip, first, blankImageTip, second}},
		},
	}
	router := gin.New()
	handler := newConfiguredLookupHandler(cache, "jp")
	router.GET("/api/v1/comics/:region/list", handler.ComicsList)
	router.GET("/api/v1/comics/:region/:id", handler.ComicsByID)
	return router
}

func comicIDs(items []shared.ComicResponse) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestComicsListKeepsOnlyTipsWithImagesInIDOrder(t *testing.T) {
	resp := serveLookupRequest(t, newComicsRouter(), http.MethodGet, "/api/v1/comics/jp/list")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var list shared.ComicListResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if ids := comicIDs(list.Items); !slices.Equal(ids, []int64{1001, 1002, 1003}) {
		t.Fatalf("expected comics in ID order, got %v", ids)
	}
	if list.Pagination.Total != 3 {
		t.Fatalf("expected the total to count only comics, got %+v", list.Pagination)
	}
}

func TestComicsListPaginatesAfterFiltering(t *testing.T) {
	router := newComicsRouter()

	resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/comics/jp/list?page=2&page_size=2")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var list shared.ComicListResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if ids := comicIDs(list.Items); !slices.Equal(ids, []int64{1003}) {
		t.Fatalf("expected the last comic on page 2, got %v", ids)
	}

	past := serveLookupRequest(t, router, http.MethodGet, "/api/v1/comics/jp/list?page=9")
	if past.Code != http.StatusOK {
		t.Fatalf("expected 200 past the end, got %d: %s", past.Code, past.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(past.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal page past the end: %v", err)
	}
	if string(raw["items"]) != "[]" {
		t.Fatalf("expected an empty items array past the end, got %s", raw["items"])
	}

	tooLarge := serveLookupRequest(t, router, http.MethodGet, "/api/v1/comics/jp/list?page_size=101")
	if tooLarge.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for page_size above 100, got %d", tooLarge.Code)
	}
}

func TestComicByIDProjectsOnlyStableFields(t *testing.T) {
	resp := serveLookupRequest(t, newComicsRouter(), http.MethodGet, "/api/v1/comics/jp/1001")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["id"] != float64(1001) || body["title"] != "First" || body["assetbundleName"] != "comic_0001" || body["fromUserRank"] != float64(1) || body["toUserRank"] != float64(10) {
		t.Fatalf("unexpected comic fields: %#v", body)
	}
	if len(body) != 5 {
		t.Fatalf("expected only five stable fields, got %#v", body)
	}
}

func TestComicByIDOmitsMissingRanks(t *testing.T) {
	resp := serveLookupRequest(t, newComicsRouter(), http.MethodGet, "/api/v1/comics/jp/1002")
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if _, exists := body["fromUserRank"]; exists {
		t.Fatalf("expected no fromUserRank, got %#v", body)
	}
}

func TestComicByIDRejectsWhatIsNotAComic(t *testing.T) {
	router := newComicsRouter()
	cases := []struct {
		name string
		path string
		code int
	}{
		{"unknown id", "/api/v1/comics/jp/9999", http.StatusNotFound},
		{"text tip", "/api/v1/comics/jp/1", http.StatusNotFound},
		{"blank image", "/api/v1/comics/jp/2", http.StatusNotFound},
		{"malformed id", "/api/v1/comics/jp/abc", http.StatusBadRequest},
		{"invalid region by id", "/api/v1/comics/xx/1001", http.StatusBadRequest},
		{"invalid region list", "/api/v1/comics/xx/list", http.StatusBadRequest},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resp := serveLookupRequest(t, router, http.MethodGet, testCase.path)
			if resp.Code != testCase.code {
				t.Fatalf("expected %d, got %d: %s", testCase.code, resp.Code, resp.Body.String())
			}
		})
	}
}
