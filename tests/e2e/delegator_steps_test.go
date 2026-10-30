package e2e_test

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"
	cp "github.com/anra-studio/anra/gen/anra/a2a/collaboration/v1"
	"github.com/anra-studio/anra/pkg/model/delegator"
	"github.com/scbizu/tape-go/pkg/runner"
	"google.golang.org/protobuf/proto"
)

type delegationVisit struct {
	peer     string
	metadata *cp.DelegationMetadata
}

func initializeDelegator(sc *godog.ScenarioContext, c *collaborationScenario) {
	var roots []*cp.DelegationMetadata
	var visits []delegationVisit
	sc.Step(`^Alice completes a ten-hop delegation and retains its full ancestry$`, c.longDelegation)
	sc.Step(`^Alice consults Bob and Charlie in separate turns$`, func() error {
		rootCalls := make(chan *cp.DelegationMetadata, 2)
		peerCalls := make(chan delegationVisit, 4)
		for _, name := range []string{"bob", "charlie"} {
			if err := c.startNode(name, modelFunc(func(ctx context.Context, _ runner.Request) (runner.Response, error) {
				peerCalls <- delegationVisit{name, delegator.FromContext(ctx).Metadata()}
				return runner.Response{Output: name + " finding"}, nil
			})); err != nil {
				return err
			}
		}
		if err := c.replaceAlice(modelFunc(func(ctx context.Context, r runner.Request) (runner.Response, error) {
			path := delegator.FromContext(ctx).Metadata()
			if len(path.Ancestors) != 0 {
				return runner.Response{}, fmt.Errorf("outgoing call mutated Alice's path: %v", path)
			}
			name, result := lastTool(r)
			if name == "" {
				rootCalls <- path
				return toolCall("send_peer_message", map[string]any{"peer": "bob", "message": "help alice"}), nil
			}
			if result["status"] != "completed" {
				return runner.Response{}, fmt.Errorf("sibling failed: %v", result)
			}
			if result["peer"] == "bob" {
				return toolCall("send_peer_message", map[string]any{"peer": "charlie", "message": "help alice too"}), nil
			}
			return runner.Response{Output: "combined siblings"}, nil
		})); err != nil {
			return err
		}
		for i := 0; i < 2; i++ {
			result, err := c.send("root", "alice", "consult both peers")
			if err != nil || result.Answer != "combined siblings" {
				return fmt.Errorf("sibling turn %d failed: %+v %v", i, result, err)
			}
		}
		roots, visits = nil, nil
		for i := 0; i < 2; i++ {
			select {
			case path := <-rootCalls:
				roots = append(roots, path)
			default:
				return fmt.Errorf("missing root turn %d", i)
			}
		}
		for i := 0; i < 4; i++ {
			select {
			case visit := <-peerCalls:
				visits = append(visits, visit)
			default:
				return fmt.Errorf("missing sibling call %d", i)
			}
		}
		return nil
	})
	sc.Step(`^sibling calls share their turn root and keep independent paths$`, func() error {
		if len(roots) != 2 || roots[0].RootRequestId == roots[1].RootRequestId {
			return fmt.Errorf("independent turns shared a root: %v", roots)
		}
		for i, visit := range visits {
			root := roots[i/2]
			path := visit.metadata
			expectedPeer := []string{"bob", "charlie"}[i%2]
			if visit.peer != expectedPeer || path.RootRequestId != root.RootRequestId || !proto.Equal(path.Deadline, root.Deadline) || len(path.Ancestors) != 1 || path.Ancestors[0] != "alice" {
				return fmt.Errorf("sibling %s inherited another branch or root: %v", visit.peer, path)
			}
		}
		return nil
	})
}

func (c *collaborationScenario) longDelegation() error {
	const hops = 10
	visited := make(chan delegationVisit, hops)
	for i := 1; i <= hops; i++ {
		name := fmt.Sprintf("hop-%d", i)
		next := fmt.Sprintf("hop-%d", i+1)
		if err := c.startNode(name, modelFunc(func(ctx context.Context, r runner.Request) (runner.Response, error) {
			tool, result := lastTool(r)
			if tool == "" {
				visited <- delegationVisit{name, delegator.FromContext(ctx).Metadata()}
				if i == hops {
					return runner.Response{Output: "finding from tenth hop"}, nil
				}
				return toolCall("send_peer_message", map[string]any{"peer": next, "message": "continue the analysis"}), nil
			}
			if result["status"] != "completed" || result["answer"] != "finding from tenth hop" {
				return runner.Response{}, fmt.Errorf("delegation stopped at %s: %v", name, result)
			}
			return runner.Response{Output: "finding from tenth hop"}, nil
		})); err != nil {
			return err
		}
	}
	if err := c.replaceAlice(modelFunc(func(_ context.Context, r runner.Request) (runner.Response, error) {
		tool, result := lastTool(r)
		if tool == "" {
			return toolCall("send_peer_message", map[string]any{"peer": "hop-1", "message": "follow this analysis chain"}), nil
		}
		if result["status"] != "completed" || result["answer"] != "finding from tenth hop" {
			return runner.Response{}, fmt.Errorf("long delegation failed: %v", result)
		}
		return runner.Response{Output: "completed ten-hop analysis"}, nil
	})); err != nil {
		return err
	}
	result, err := c.send("root", "alice", "start long analysis")
	if err != nil || result.Answer != "completed ten-hop analysis" {
		return fmt.Errorf("long delegation did not complete: %+v %v", result, err)
	}
	var root *cp.DelegationMetadata
	expected := []string{"alice"}
	for i := 1; i <= hops; i++ {
		select {
		case visit := <-visited:
			if root == nil {
				root = visit.metadata
			}
			path := visit.metadata
			if visit.peer != fmt.Sprintf("hop-%d", i) || path.RootRequestId == "" || path.RootRequestId != root.RootRequestId || !proto.Equal(path.Deadline, root.Deadline) || len(path.Ancestors) != len(expected) {
				return fmt.Errorf("hop %d lost its root or ancestry: %v", i, path)
			}
			for j, name := range expected {
				if path.Ancestors[j] != name {
					return fmt.Errorf("hop %d has wrong ancestry: %v", i, path.Ancestors)
				}
			}
			expected = append(expected, visit.peer)
		default:
			return fmt.Errorf("missing delegation hop %d", i)
		}
	}
	for _, node := range c.nodes {
		entries, err := c.readEntries(node)
		if err != nil {
			return err
		}
		if _, err := inspectLifecycle(entries); err != nil {
			return err
		}
	}
	return nil
}
