// Package peer validates the shared Peer model independently of RPC adapters.
package peer

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"buf.build/go/protovalidate"
	pv "github.com/scbizu/anra/gen/anra/peer/v1"
	"google.golang.org/protobuf/proto"
)

const Chat = "chat"
const BorrowContext = "borrow-context"

// Address normalizes a public HTTP(S) origin. Listen addresses are resolved by
// the host before publication; wildcard addresses and port zero are unusable.
func Address(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil {
		return "", errors.New("peer: address must be an HTTP(S) origin")
	}
	if u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.Opaque != "" {
		return "", errors.New("peer: address must be an HTTP(S) origin")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsUnspecified() {
		return "", errors.New("peer: address must not be a wildcard")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("peer: address must use a nonzero valid port")
		}
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

// Normalize validates and copies metadata. Storage never retains caller-owned
// protobuf values and validates the full snapshot before a state transition.
func Normalize(value *pv.Peer) (*pv.Peer, error) {
	if value == nil {
		return nil, errors.New("peer: metadata is required")
	}
	if err := protovalidate.Validate(value); err != nil {
		return nil, fmt.Errorf("peer: %w", err)
	}
	address, err := Address(value.Address)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, skill := range value.Skill {
		if skill == nil || strings.TrimSpace(skill.Id) == "" || strings.TrimSpace(skill.Name) == "" {
			return nil, errors.New("peer: skill ID and name are required")
		}
		if seen[skill.Id] {
			return nil, errors.New("peer: duplicate skill ID")
		}
		seen[skill.Id] = true
	}
	copy := proto.Clone(value).(*pv.Peer)
	copy.Address = address
	return copy, nil
}

func HasSkill(value *pv.Peer, id string) bool {
	for _, skill := range value.GetSkill() {
		if skill.GetId() == id {
			return true
		}
	}
	return false
}
