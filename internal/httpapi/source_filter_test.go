package httpapi

import (
	"strings"
	"testing"
	"wzstudy/internal/study"
)

func TestSourceFilterNormalizesUUID(t *testing.T) {
	_, h := app()
	source, key := create(t, h, true)
	ingest(t, h, source.ID, key, "normalized")
	var page struct {
		Items []study.PublicItem `json:"items"`
	}
	data(t, request(t, h, "GET", "/api/v1/items?source_id="+strings.ToUpper(source.ID), readKey, nil), &page)
	if len(page.Items) != 1 {
		t.Fatal("upper-case UUID filter lost records")
	}
	data(t, request(t, h, "GET", "/api/v1/items?source_id="+strings.ReplaceAll(source.ID, "-", ""), readKey, nil), &page)
	if len(page.Items) != 1 {
		t.Fatal("compact UUID filter lost records")
	}
}
