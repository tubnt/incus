// Package httpx hosts shared HTTP-layer helpers (request decoding + validation,
// response shaping) used across handler subpackages. Keeps portal/admin/auth
// packages from each having to define the same boilerplate.
package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

// SafeNameRe mirrors the historical regex used across handler path/body
// validation. Exported so callers doing non-struct checks (e.g. `chi.URLParam`
// validation) can reuse it instead of redeclaring their own.
var SafeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// IsValidName is the package-level version of the same historical helper.
// 1–64 chars, alnum-led, `[A-Za-z0-9._-]+`.
func IsValidName(s string) bool {
	return s != "" && len(s) <= 64 && SafeNameRe.MatchString(s)
}

var (
	validatorOnce     sync.Once
	validatorInstance *validator.Validate
)

// Validator returns the shared validator instance. Lazily initialised on first
// call; safe for concurrent use. Custom tags (safename) are registered here so
// every importer gets the same behaviour.
func Validator() *validator.Validate {
	validatorOnce.Do(func() {
		v := validator.New(validator.WithRequiredStructEnabled())
		_ = v.RegisterValidation("safename", func(fl validator.FieldLevel) bool {
			return IsValidName(fl.Field().String())
		})
		validatorInstance = v
	})
	return validatorInstance
}

// DecodeAndValidate unmarshals the request body into dst and runs struct
// validation. On failure a 400 JSON error response is written and the caller
// should return. Ok → true, caller proceeds.
//
// Errors are shaped as:
//
//	{"error": "invalid body: ..."}          // JSON decode failure
//	{"error": "validation_failed", "details": ["field: tag(param)"]}  // validator failure
func DecodeAndValidate(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, map[string]any{"error": "invalid body: " + err.Error()})
		return false
	}
	if err := Validator().Struct(dst); err != nil {
		writeError(w, http.StatusBadRequest, map[string]any{
			"error":   "validation_failed",
			"details": formatValidationErr(err),
		})
		return false
	}
	return true
}

// formatValidationErr expands validator.ValidationErrors into `field: tag(param)`
// strings for direct UI display. CamelCase → camelCase to match JSON convention
// without parsing struct tags.
func formatValidationErr(err error) []string {
	ves, ok := err.(validator.ValidationErrors)
	if !ok {
		return []string{err.Error()}
	}
	out := make([]string, 0, len(ves))
	for _, fe := range ves {
		field := fe.Field()
		if field != "" {
			field = strings.ToLower(field[:1]) + field[1:]
		}
		if fe.Param() != "" {
			out = append(out, fmt.Sprintf("%s: %s(%s)", field, fe.Tag(), fe.Param()))
		} else {
			out = append(out, fmt.Sprintf("%s: %s", field, fe.Tag()))
		}
	}
	return out
}

// writeError is inlined here (instead of importing a shared writeJSON) so this
// package has no reverse dependency on caller packages.
func writeError(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
