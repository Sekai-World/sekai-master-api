package shared

import "testing"

func TestPaginateItemsCapsPageSize(t *testing.T) {
	items := make([]map[string]any, 150)
	for index := range items {
		items[index] = map[string]any{"id": index + 1}
	}

	paged, pagination := PaginateItems(items, 1, 101)

	if len(paged) != 100 {
		t.Fatalf("expected page size to be capped at 100, got %d", len(paged))
	}
	if pagination["page_size"] != 100 {
		t.Fatalf("expected pagination.page_size=100, got %v", pagination["page_size"])
	}
	if pagination["total_pages"] != 2 {
		t.Fatalf("expected pagination.total_pages=2, got %v", pagination["total_pages"])
	}
	if pagination["has_next"] != true {
		t.Fatalf("expected pagination.has_next=true, got %v", pagination["has_next"])
	}
}
