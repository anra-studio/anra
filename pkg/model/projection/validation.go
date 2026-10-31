package projection

import (
	"encoding/json"

	"github.com/go-playground/validator/v10"
)

var validate = func() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	if err := v.RegisterValidation("jsonobject", func(f validator.FieldLevel) bool {
		var object map[string]json.RawMessage
		return json.Unmarshal(f.Field().Bytes(), &object) == nil && object != nil
	}); err != nil {
		panic(err)
	}
	return v
}()
