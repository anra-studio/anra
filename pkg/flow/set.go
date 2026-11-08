// Package flow defines the concrete flows composed by an embedding host.
// Child packages own their behavior and never import this composition package.
package flow

import (
	"github.com/anra-studio/anra/pkg/flow/borrow"
	"github.com/anra-studio/anra/pkg/flow/clbr"
	"github.com/anra-studio/anra/pkg/flow/grant"
)

// Set groups independently usable flows. It owns no storage or background work;
// the host supplies dependencies and drains operations before closing storage.
type Set struct {
	Borrow *borrow.Service
	Clbr   clbr.Service
	Grant  *grant.Service
}
