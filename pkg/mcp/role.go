package mcp

import (
	"context"
	"errors"
	"strings"
)

type RoleRef struct {
	ID      string `json:"id" validate:"required,max=256"`
	Version string `json:"version" validate:"required,max=256"`
}

// RoleDefinition is the complete, reproducible definition of one role revision,
// not a live lookup by display name. The caller chooses a version and cannot
// overwrite that version.
type RoleDefinition struct {
	RoleRef
	Profile Profile `json:"profile" validate:"required"`
}

// Profile describes what a role is for and what it is equipped to do. Add new
// facets of a role here; String is the single place that turns them into the
// instructions for an external runtime.
type Profile struct {
	Purpose      string `json:"purpose" validate:"required,max=1024"`
	Instructions string `json:"instructions" validate:"required"`
	Tools        []Tool `json:"tools,omitempty" validate:"dive"`
}

// Tool names a tool the role is proficient with and the permissions it needs.
type Tool struct {
	Name        string   `json:"name" validate:"required,max=256"`
	Permissions []string `json:"permissions,omitempty" validate:"dive,required,max=256"`
}

// String renders the instructions an external runtime should follow.
func (p Profile) String() string {
	var b strings.Builder
	b.WriteString(p.Purpose)
	b.WriteString("\n\n")
	b.WriteString(p.Instructions)
	if len(p.Tools) > 0 {
		b.WriteString("\n\nTools:")
		for _, t := range p.Tools {
			b.WriteString("\n- ")
			b.WriteString(t.Name)
			if len(t.Permissions) > 0 {
				b.WriteString(" (" + strings.Join(t.Permissions, ", ") + ")")
			}
		}
	}
	return b.String()
}

func (s *Service) role(ctx context.Context, ref RoleRef) (*RoleDefinition, error) {
	d, err := s.config.Roles.GetRole(ctx, ref)
	if errors.Is(err, ErrInvalidReference) {
		return nil, ErrInvalidReference
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	return d, nil
}
