package projection

import (
	"bytes"
	"encoding/json"

	"github.com/go-playground/validator/v10"
)

var validate = func() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	if err := v.RegisterValidation("jsonobject", func(f validator.FieldLevel) bool {
		data := bytes.TrimSpace(f.Field().Bytes())
		return len(data) >= 2 && data[0] == '{' && data[len(data)-1] == '}' && json.Valid(f.Field().Bytes())
	}); err != nil {
		panic(err)
	}
	return v
}()
