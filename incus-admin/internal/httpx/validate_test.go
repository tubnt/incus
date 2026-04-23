package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type sampleReq struct {
	Name   string  `json:"name"   validate:"required,safename"`
	Count  int     `json:"count"  validate:"required,gte=1,lte=10"`
	Amount float64 `json:"amount" validate:"gte=0,lte=100"`
	Role   string  `json:"role"   validate:"required,oneof=admin customer"`
}

// newReq 构造一个带 JSON body 的 httptest.Request；body 可以是结构体或 raw string。
func newReq(t *testing.T, body any) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	switch v := body.(type) {
	case string:
		buf.WriteString(v)
	default:
		if err := json.NewEncoder(&buf).Encode(v); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	return httptest.NewRequest(http.MethodPost, "/", &buf)
}

func TestDecodeAndValidate(t *testing.T) {
	tests := []struct {
		name      string
		body      any
		wantOK    bool
		wantCode  int
		wantInMsg string
	}{
		{
			name:   "happy path",
			body:   sampleReq{Name: "foo", Count: 3, Amount: 42.5, Role: "admin"},
			wantOK: true,
		},
		{
			name:      "malformed json",
			body:      "{not-json",
			wantOK:    false,
			wantCode:  http.StatusBadRequest,
			wantInMsg: "invalid body",
		},
		{
			name:      "missing required",
			body:      sampleReq{Name: "foo", Count: 3, Amount: 10},
			wantOK:    false,
			wantCode:  http.StatusBadRequest,
			wantInMsg: "role: required",
		},
		{
			name:      "safename rejects special chars",
			body:      sampleReq{Name: "foo bar", Count: 3, Amount: 10, Role: "admin"},
			wantOK:    false,
			wantCode:  http.StatusBadRequest,
			wantInMsg: "name: safename",
		},
		{
			name:      "oneof rejects unknown role",
			body:      sampleReq{Name: "foo", Count: 3, Amount: 10, Role: "root"},
			wantOK:    false,
			wantCode:  http.StatusBadRequest,
			wantInMsg: "role: oneof",
		},
		{
			name:      "lte bound",
			body:      sampleReq{Name: "foo", Count: 99, Amount: 10, Role: "admin"},
			wantOK:    false,
			wantCode:  http.StatusBadRequest,
			wantInMsg: "count: lte(10)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := newReq(t, tc.body)
			var dst sampleReq
			ok := DecodeAndValidate(w, r, &dst)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (body=%s)", ok, tc.wantOK, w.Body.String())
			}
			if tc.wantOK {
				return
			}
			if w.Code != tc.wantCode {
				t.Fatalf("status=%d want %d", w.Code, tc.wantCode)
			}
			if tc.wantInMsg != "" && !strings.Contains(w.Body.String(), tc.wantInMsg) {
				t.Fatalf("body %q missing %q", w.Body.String(), tc.wantInMsg)
			}
		})
	}
}

func TestIsValidName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"a", true},
		{"abc-123_xyz.ok", true},
		{"-starts-dash", false},
		{"has space", false},
		{"has/slash", false},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
	}
	for _, c := range cases {
		if got := IsValidName(c.in); got != c.want {
			t.Errorf("IsValidName(%q)=%v want %v", c.in, got, c.want)
		}
	}
}
