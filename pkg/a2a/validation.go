package a2a

import (
	"fmt"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
)

func validateMessage(value proto.Message) error {
	if value == nil || !value.ProtoReflect().IsValid() {
		return ErrInvalidParams
	}
	if err := protovalidate.Validate(value); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidParams, err)
	}
	return nil
}
