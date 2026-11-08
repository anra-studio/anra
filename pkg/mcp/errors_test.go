package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/anra-studio/anra/pkg/flow/borrow"
	"github.com/anra-studio/anra/pkg/model/view"
)

func TestWrapError(t *testing.T) {
	cause := fmt.Errorf("private detail: %w", view.ErrUnsupported)
	err := wrapError(ErrFailedPrecondition, cause)
	if !errors.Is(err, view.ErrUnsupported) || !errors.Is(err, ErrFailedPrecondition) {
		t.Fatal("wrapped error must retain both public error and underlying cause")
	}
	if err.Error() != ErrFailedPrecondition.Error() {
		t.Fatal("error message must not expose the cause")
	}
	data, marshalErr := json.Marshal(ErrorResponse{Error: err})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	expected, marshalErr := json.Marshal(ErrorResponse{Error: ErrFailedPrecondition})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if string(data) != string(expected) {
		t.Fatalf("response exposes cause: %s", data)
	}
}

func TestToViewError(t *testing.T) {
	unknown := errors.New("private storage failure")
	for _, tc := range []struct {
		cause  error
		public *Error
	}{
		{
			cause:  borrow.ErrUnavailable,
			public: ErrPermissionDenied,
		},
		{
			cause:  view.ErrUnsupported,
			public: ErrFailedPrecondition,
		},
		{
			cause:  unknown,
			public: ErrUnavailable,
		},
	} {
		err := toViewError(fmt.Errorf("read view: %w", tc.cause))
		if !errors.Is(err, tc.cause) || !errors.Is(err, tc.public) {
			t.Errorf("toViewError(%v) = %v; want cause and %v", tc.cause, err, tc.public)
		}
	}
}
