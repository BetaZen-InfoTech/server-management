package validator

import (
	"github.com/go-playground/validator/v10"
)

var validate = validator.New()

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
