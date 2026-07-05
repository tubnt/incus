package v1

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePagination_Defaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/instances", nil)
	page, pageSize, err := parsePagination(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if page != 1 || pageSize != 25 {
		t.Fatalf("got page=%d page_size=%d, want 1/25", page, pageSize)
	}
}

func TestParsePagination_Explicit(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/instances?page=3&page_size=50", nil)
	page, pageSize, err := parsePagination(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if page != 3 || pageSize != 50 {
		t.Fatalf("got page=%d page_size=%d, want 3/50", page, pageSize)
	}
}

func TestParsePagination_AtMax(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/instances?page_size=100", nil)
	_, pageSize, err := parsePagination(req)
	if err != nil {
		t.Fatalf("page_size=100 should be allowed; got err %v", err)
	}
	if pageSize != 100 {
		t.Fatalf("page_size = %d, want 100", pageSize)
	}
}

func TestParsePagination_OverMax(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/instances?page_size=101", nil)
	_, _, err := parsePagination(req)
	if err == nil {
		t.Fatal("expected err for page_size=101")
	}
	var pe *paginationErr
	if !errors.As(err, &pe) || pe.Field != "page_size" || pe.Reason != "exceeds_max" {
		t.Fatalf("got %v, want paginationErr{page_size,exceeds_max}", err)
	}
}

func TestParsePagination_InvalidPage(t *testing.T) {
	cases := []string{"abc", "0", "-1", "1.5"}
	for _, v := range cases {
		req := httptest.NewRequest(http.MethodGet, "/v1/instances?page="+v, nil)
		_, _, err := parsePagination(req)
		if err == nil {
			t.Errorf("page=%q should have errored", v)
			continue
		}
		var pe *paginationErr
		if !errors.As(err, &pe) || pe.Field != "page" || pe.Reason != "invalid" {
			t.Errorf("page=%q got %v, want paginationErr{page,invalid}", v, err)
		}
	}
}

func TestParsePagination_InvalidPageSize(t *testing.T) {
	cases := []string{"abc", "0", "-5"}
	for _, v := range cases {
		req := httptest.NewRequest(http.MethodGet, "/v1/instances?page_size="+v, nil)
		_, _, err := parsePagination(req)
		if err == nil {
			t.Errorf("page_size=%q should have errored", v)
			continue
		}
		var pe *paginationErr
		if !errors.As(err, &pe) || pe.Field != "page_size" || pe.Reason != "invalid" {
			t.Errorf("page_size=%q got %v, want paginationErr{page_size,invalid}", v, err)
		}
	}
}

func TestWritePaginationErr_StructuredError(t *testing.T) {
	rr := httptest.NewRecorder()
	writePaginationErr(rr, &paginationErr{Field: "page_size", Reason: "exceeds_max"})

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
	var body struct {
		Errors []FieldError `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0].Field != "page_size" || body.Errors[0].Reason != "exceeds_max" {
		t.Fatalf("got %+v", body.Errors)
	}
}

func TestWritePage_TotalZero(t *testing.T) {
	rr := httptest.NewRecorder()
	writePage(rr, []string{}, 1, 25, 0)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body struct {
		Data     []string `json:"data"`
		Page     int      `json:"page"`
		PageSize int      `json:"page_size"`
		Pages    int      `json:"pages"`
		Total    int      `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Pages != 0 || body.Total != 0 {
		t.Fatalf("got pages=%d total=%d, want 0/0", body.Pages, body.Total)
	}
}

func TestWritePage_PagesCeiling(t *testing.T) {
	// 100 items / page_size 25 = 4 pages
	rr := httptest.NewRecorder()
	writePage(rr, []int{1, 2, 3}, 2, 25, 100)
	var body struct {
		Pages    int `json:"pages"`
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
		Total    int `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Pages != 4 || body.Page != 2 || body.PageSize != 25 || body.Total != 100 {
		t.Fatalf("got %+v, want pages=4 page=2 page_size=25 total=100", body)
	}

	// 101 items / 25 = 5 (ceiling)
	rr = httptest.NewRecorder()
	writePage(rr, nil, 1, 25, 101)
	body = struct {
		Pages    int `json:"pages"`
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
		Total    int `json:"total"`
	}{}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Pages != 5 {
		t.Fatalf("101/25 should be 5 pages, got %d", body.Pages)
	}
}

func TestWritePage_NegativeTotalClamped(t *testing.T) {
	rr := httptest.NewRecorder()
	writePage(rr, nil, 1, 25, -100)
	var body struct {
		Total int `json:"total"`
		Pages int `json:"pages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Total != 0 || body.Pages != 0 {
		t.Fatalf("got total=%d pages=%d, want 0/0", body.Total, body.Pages)
	}
}
