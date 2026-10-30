package anchor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"buf.build/go/protovalidate"
	"github.com/go-playground/validator/v10"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

// Schema identifies native execution records in the custom anchor envelope.
const Schema = "anra.dev/anchor/v1"

// Type identifies the native execution transition represented by a record.
// Its string values are part of the persisted anchor format.
type Type string

const (
	TapeInitialized Type = "tape.initialized"
	TurnStarted     Type = "turn.started"
	TurnClosed      Type = "turn.closed"
)

// Record describes tape initialization or an execution turn transition.
type Record struct {
	Schema     string       `json:"schema" validate:"eq=anra.dev/anchor/v1"`
	Type       Type         `json:"type" validate:"oneof=tape.initialized turn.started turn.closed"`
	Source     *pb.Source   `json:"source,omitempty" validate:"required_if=Type tape.initialized,excluded_unless=Type tape.initialized,omitempty,structonly"`
	TurnID     string       `json:"turnId,omitempty" validate:"required_unless=Type tape.initialized,excluded_if=Type tape.initialized,maxbytes=256"`
	OwnerEpoch string       `json:"ownerEpoch,omitempty" validate:"required_if=Type turn.started,excluded_if=Type tape.initialized,maxbytes=256"`
	BaseView   *pb.TapeView `json:"baseView,omitempty" validate:"excluded_unless=Type turn.started,omitempty,structonly"`
	StartSeq   string       `json:"startSeq,omitempty" validate:"required_if=Type turn.closed,excluded_unless=Type turn.closed"`
	Outcome    string       `json:"outcome,omitempty" validate:"required_if=Type turn.closed,excluded_unless=Type turn.closed,omitempty,oneof=completed failed cancelled abandoned"`
}

var recordValidator = func() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	// validator's built-in string max counts runes. Stored identities have a
	// byte limit, so preserve that wire-format constraint explicitly.
	if err := v.RegisterValidation("maxbytes", func(fl validator.FieldLevel) bool {
		limit, err := strconv.Atoi(fl.Param())
		return err == nil && len(fl.Field().String()) <= limit
	}); err != nil {
		panic(err)
	}
	return v
}()

func decodeRecord(e te.EntryLike, raw []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if err := recordValidator.Struct(r); err != nil {
		return nil, fmt.Errorf("anchor: %w", err)
	}
	if r.Source != nil {
		if err := protovalidate.Validate(r.Source); err != nil {
			return nil, fmt.Errorf("anchor: %w", err)
		}
	}
	if r.Type == TurnClosed {
		start, err := te.ParseSeq(r.StartSeq)
		if err != nil || start.IsZero() || start.Cmp(e.GetID()) >= 0 {
			return nil, errors.New("anchor: invalid turn reference")
		}
		if r.Outcome == "completed" && start.Next().Cmp(e.GetID()) >= 0 {
			return nil, errors.New("anchor: empty completed context")
		}
	}
	return &r, nil
}
