package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteErr(t *testing.T) {
	rr := httptest.NewRecorder()
	writeErr(rr, http.StatusBadRequest, "name", "required")

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/json; charset=utf-8", ct)
	}

	var body struct {
		Errors []FieldError `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(body.Errors) != 1 {
		t.Fatalf("errors len = %d, want 1", len(body.Errors))
	}
	if body.Errors[0].Field != "name" || body.Errors[0].Reason != "required" {
		t.Fatalf("got %+v, want {Field:name Reason:required}", body.Errors[0])
	}
}

func TestWriteErr_EmptyFieldIsGeneric(t *testing.T) {
	rr := httptest.NewRecorder()
	writeErr(rr, http.StatusUnauthorized, "", "unauthorized")

	var body struct {
		Errors []FieldError `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0].Field != "" || body.Errors[0].Reason != "unauthorized" {
		t.Fatalf("got %+v, want field empty + reason unauthorized", body.Errors)
	}
}

func TestWriteErrs_Multi(t *testing.T) {
	rr := httptest.NewRecorder()
	writeErrs(rr, http.StatusUnprocessableEntity, []FieldError{
		{Field: "page", Reason: "invalid"},
		{Field: "page_size", Reason: "exceeds_max"},
	})

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
	var body struct {
		Errors []FieldError `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Errors) != 2 {
		t.Fatalf("errors len = %d, want 2", len(body.Errors))
	}
	if body.Errors[0].Field != "page" || body.Errors[1].Reason != "exceeds_max" {
		t.Fatalf("unexpected errors: %+v", body.Errors)
	}
}

func TestWriteErrs_EmptyFallsBackToUnknown(t *testing.T) {
	rr := httptest.NewRecorder()
	writeErrs(rr, http.StatusInternalServerError, nil)

	var body struct {
		Errors []FieldError `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0].Reason != "unknown" {
		t.Fatalf("expected fallback unknown error, got %+v", body.Errors)
	}
}

func TestNotImplemented_Placeholder(t *testing.T) {
	h := New()
	req := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	rr := httptest.NewRecorder()
	h.notImplemented(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rr.Code)
	}
	var body struct {
		Errors []FieldError `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0].Reason != "not_implemented" {
		t.Fatalf("got %+v, want reason=not_implemented", body.Errors)
	}
}
