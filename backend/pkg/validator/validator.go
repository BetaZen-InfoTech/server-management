package validator

import (
	"regexp"

	"github.com/go-playground/validator/v10"
)

var validate = validator.New()

// safeDNSNameRe matches a shell-safe hostname / domain / DNS-zone token:
// letters, digits, dot, hyphen and underscore only. It deliberately rejects
// every shell metacharacter (single/double quote, ;, |, &, $, backtick,
// parentheses, whitespace, newline …) so a value that passes can be
// interpolated into a shell command (echo '…', sed 's/…/', etc.) without
// breaking out of its quoting context.
var safeDNSNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// safeEmailRe is a strict, shell-safe email pattern. It is intentionally
// stricter than RFC 5321 — it rejects quoted local-parts and any character
// outside the conservative set below — because go-playground's "email" rule
// accepts RFC-legal single quotes that break out of an echo '…' context.
var safeEmailRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9._-]+$`)

// IsSafeDNSName reports whether s is a non-empty, ≤253-char hostname/domain
// token containing only letters, digits, dot, hyphen and underscore. Callers
// that shell out with a domain/hostname MUST gate on this to prevent
// authenticated command injection (see security audit §8).
func IsSafeDNSName(s string) bool {
	return s != "" && len(s) <= 253 && safeDNSNameRe.MatchString(s)
}

// IsSafeEmail reports whether s is a shell-safe email address (stricter than
// the "email" struct tag — no quoted local-parts, no shell metacharacters).
func IsSafeEmail(s string) bool {
	return len(s) >= 3 && len(s) <= 320 && safeEmailRe.MatchString(s)
}

func Validate(s interface{}) []FieldError {
	err := validate.Struct(s)
	if err == nil {
		return nil
	}

	var errors []FieldError
	for _, e := range err.(validator.ValidationErrors) {
		errors = append(errors, FieldError{
			Field:   e.Field(),
			Message: msgForTag(e),
		})
	}
	return errors
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func msgForTag(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "This field is required"
	case "email":
		return "Must be a valid email address"
	case "min":
		return "Must be at least " + fe.Param() + " characters"
	case "max":
		return "Must be at most " + fe.Param() + " characters"
	case "oneof":
		return "Must be one of: " + fe.Param()
	default:
		return "Invalid value"
	}
}
