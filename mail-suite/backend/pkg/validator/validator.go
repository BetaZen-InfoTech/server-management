package validator

import (
	"fmt"
	"strings"

	v10 "github.com/go-playground/validator/v10"
)

var v = v10.New()

func Struct(s interface{}) error {
	if err := v.Struct(s); err != nil {
		if ve, ok := err.(v10.ValidationErrors); ok {
			msgs := make([]string, 0, len(ve))
			for _, fe := range ve {
				msgs = append(msgs, fmt.Sprintf("%s: %s", fe.Field(), fe.Tag()))
			}
			return fmt.Errorf("%s", strings.Join(msgs, "; "))
		}
		return err
	}
	return nil
}
