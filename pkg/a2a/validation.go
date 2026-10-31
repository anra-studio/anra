package a2a

import (
	"buf.build/go/protovalidate"
	"encoding/json"
	"fmt"
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/go-playground/validator/v10"
	"google.golang.org/protobuf/proto"
)

var structValidator = validator.New(validator.WithRequiredStructEnabled())

func validateStruct(value any) error {
	if err := structValidator.Struct(value); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidParams, err)
	}
	return nil
}
func validateMessage(value proto.Message) error {
	if value == nil || !value.ProtoReflect().IsValid() {
		return ErrInvalidParams
	}
	if err := protovalidate.Validate(value); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidParams, err)
	}
	return nil
}

// JSON syntax is a payload-specific check; message structure is schema-defined.
func validatePayloadJSON(p *pb.Payload) error {
	if p != nil && !json.Valid(p.JsonData) {
		return ErrInvalidParams
	}
	return nil
}

func validateEntry(e *pb.ExportedEntry) error {
	if err := validateMessage(e); err != nil {
		return err
	}
	if err := e.Timestamp.CheckValid(); err != nil {
		return ErrInvalidParams
	}
	return validatePayloadJSON(e.Payload)
}
