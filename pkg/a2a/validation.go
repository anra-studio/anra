package a2a

import (
	"buf.build/go/protovalidate"
	"errors"
	"github.com/go-playground/validator/v10"
	"google.golang.org/protobuf/proto"
)

var structValidator = newStructValidator()

func newStructValidator() *validator.Validate {
	return validator.New(validator.WithRequiredStructEnabled())
}
func validateStruct(value any) error {
	if err := structValidator.Struct(value); err != nil {
		return errors.Join(ErrInvalidParams, err)
	}
	return nil
}
func validateMessage(value proto.Message) error {
	if value == nil || !value.ProtoReflect().IsValid() {
		return ErrInvalidParams
	}
	if err := protovalidate.Validate(value); err != nil {
		return errors.Join(ErrInvalidParams, err)
	}
	return nil
}
