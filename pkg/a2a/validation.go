package a2a

import (
	"errors"
	"strconv"

	"github.com/go-playground/validator/v10"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

// Register rules once, before the validator is shared by concurrent callers.
var structValidator = newStructValidator()

func newStructValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	// Built-in string max counts runes; our API and protobuf rules count bytes.
	if err := v.RegisterValidation("maxbytes", func(field validator.FieldLevel) bool {
		limit, err := strconv.Atoi(field.Param())
		return err == nil && len(field.Field().String()) <= limit
	}); err != nil {
		panic(err)
	}
	v.RegisterStructValidation(func(level validator.StructLevel) {
		r := level.Current().Interface().(tv.EntryRange)
		if r.SeqS.IsZero() {
			level.ReportError(r.SeqS.String(), "SeqS", "SeqS", "positive", "")
		}
		if r.SeqS.Cmp(r.SeqE) >= 0 {
			level.ReportError(r.SeqE.String(), "SeqE", "SeqE", "after_start", "")
		}
	}, tv.EntryRange{})
	return v
}

func validateStruct(value any) error {
	if err := structValidator.Struct(value); err != nil {
		return errors.Join(ErrInvalidParams, err)
	}
	return nil
}
