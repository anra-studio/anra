package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"path/filepath"
	"strings"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/cucumber/godog"
	cp "github.com/anra-studio/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/agent"
	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	"github.com/scbizu/tape-go/pkg/tape/view"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
	"google.golang.org/protobuf/proto"
)

func initializeRecovery(sc *godog.ScenarioContext, team *teamScenario) {
	var ref *pb.TapeView
	var grant *pb.Decision
	var card *sdk.AgentCard
	var stopped chan error
	c := team.c
	sc.Step(`^I try to ask running agent "([^"]*)" to "([^"]*)"$`, func(name, input string) { c.execute("ask", "--agent", name, "--config", c.configPath(), input) })
	sc.Step(`^Alice is interrupted while reasoning after a published answer$`, func() error {
		c.execute("ask", "--agent", "alice", "--config", c.configPath(), "publish before crash")
		if c.err != nil {
			return c.err
		}
		stopped = make(chan error, 1)
		go func() {
			client, err := messageClient(c.s.ctx, a2a.TrustedHTTPClient("bob"), team.agents["alice"].peer)
			if err == nil {
				_, err = client.Send(c.s.ctx, "crash before completion")
			}
			stopped <- err
		}()
		select {
		case <-c.s.providerControl.entered:
		case <-c.s.ctx.Done():
			return c.s.ctx.Err()
		}
		client, err := messageClient(c.s.ctx, a2a.TrustedHTTPClient("bob"), team.agents["alice"].peer)
		if err != nil {
			return err
		}
		result, err := client.Send(c.s.ctx, "callback before crash")
		if err != nil {
			return err
		}
		if result.Status != cp.Status_STATUS_BUSY || result.View == nil {
			return fmt.Errorf("expected busy published source: %+v", result)
		}
		ref = result.View
		card = client.Card
		views, err := a2a.NewClient(a2a.TrustedHTTPClient("bob"), card)
		if err != nil {
			return err
		}
		grant, err = views.Borrow(c.s.ctx, &pb.BorrowRequest{RequestId: "crash-grant", Purpose: "resume research", Query: "published answer", View: ref})
		if err != nil {
			return err
		}
		if grant.Status != a2a.Granted {
			return errors.New("precrash grant denied")
		}
		return nil
	})
	sc.Step(`^the interrupted tape retains an unmatched tool call and state delta$`, func() error {
		c.s.providerControl.unblock()
		select {
		case err := <-stopped:
			if err == nil {
				return errors.New("killed request succeeded")
			}
		case <-c.s.ctx.Done():
			return c.s.ctx.Err()
		}
		tape, err := bbolt.NewBboltStorage("default", filepath.Join(c.dataDir(), "alice.db"))
		if err != nil {
			return err
		}
		defer tape.Close()
		ctx := owner.WithOwnerId(c.s.ctx, "alice")
		if err = tape.Init(ctx); err != nil {
			return err
		}
		event := session.NewEvent("interrupted-invocation")
		event.Author = "anra"
		event.Content = &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "interrupted-call", Name: "send_peer_message", Args: map[string]any{"peer": "bob", "message": "unfinished"}}}}}
		event.Actions.StateDelta = map[string]any{"interrupted_state": "must not replay"}
		encoded, err := json.Marshal(event)
		if err != nil {
			return err
		}
		_, err = tape.Store(ctx, entry.CustomEntry{Entry: entry.NewEntry(entry.WithEntryOwner("alice"), entry.WithEntryKind(entry.EntryToolCall)), Extensions: map[string]any{"adk:event": string(encoded)}})
		return err
	})
	sc.Step(`^the recovered agent keeps its source, view, grant, and physical history$`, func() error {
		c.execute("ask", "--agent", "alice", "--config", c.configPath(), "verify recovered history")
		if c.err != nil {
			return fmt.Errorf("recovered model failed: %s %v", c.output, c.err)
		}
		if strings.TrimSpace(c.output) != "alice: verify recovered history" {
			return fmt.Errorf("recovered answer: %s", c.output)
		}
		client, err := messageClient(c.s.ctx, a2a.TrustedHTTPClient("bob"), team.agents["alice"].peer)
		if err != nil {
			return err
		}
		views, err := a2a.NewClient(a2a.TrustedHTTPClient("bob"), client.Card)
		if err != nil {
			return err
		}
		pages, err := views.ReadView(c.s.ctx, grant.View)
		if err != nil {
			return err
		}
		if len(pages) != 1 || len(pages[0].Entries) != 2 || pages[0].Entries[0].Summary != "publish before crash" || pages[0].Entries[1].Summary != "alice: publish before crash" {
			return errors.New("precrash view changed")
		}
		return nil
	})
	sc.Step(`^exactly one abandoned close excludes the unfinished invocation$`, func() error {
		tape, err := bbolt.NewBboltStorage("default", filepath.Join(c.dataDir(), "alice.db"))
		if err != nil {
			return err
		}
		defer tape.Close()
		ctx := owner.WithOwnerId(c.s.ctx, "alice")
		if err = tape.Init(ctx); err != nil {
			return err
		}
		h, err := tape.Get(ctx)
		if err != nil {
			return err
		}
		entries, err := tape.Range(ctx, view.EntryRange{SeqS: entry.SeqFromUint64(1), SeqE: h.Scope.SeqE.Next()})
		if err != nil {
			return err
		}
		abandoned, unmatched := 0, 0
		for _, e := range entries.Raw {
			if e.GetKind() == entry.EntryUser && e.GetSummary() == "rejected CLI callback" {
				return errors.New("BUSY CLI request entered the conversation")
			}
			r, err := anchor.Decode(e)
			if err != nil {
				return err
			}
			if r != nil {
				if r.Type == "tape.initialized" && !proto.Equal(r.Source, ref.Source) {
					return errors.New("source changed on recovery")
				}
				if r.Type == "invocation.closed" && r.Outcome == "abandoned" {
					abandoned++
				}
			}
			var custom entry.CustomEntry
			switch v := e.(type) {
			case entry.CustomEntry:
				custom = v
			case *entry.CustomEntry:
				custom = *v
			}
			if raw, ok := custom.Extensions["adk:event"].(string); ok && strings.Contains(raw, "interrupted-call") {
				unmatched++
			}
		}
		if abandoned != 1 || unmatched != 1 {
			return fmt.Errorf("recovery changed physical history: abandoned=%d interrupted=%d", abandoned, unmatched)
		}
		// Load through the public runtime adapter with a model that checks session state.
		a, err := agent.New("alice", tape, agent.WithLLM(stateCheckingModel{}))
		if err != nil {
			return err
		}
		_, err = a.Run(c.s.ctx, "check state after recovery")
		return err
	})
}

type stateCheckingModel struct{}

func (stateCheckingModel) Name() string { return "state-check" }
func (stateCheckingModel) GenerateContent(ctx context.Context, r *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		invocation, ok := ctx.(adkagent.InvocationContext)
		if !ok {
			yield(nil, errors.New("model fixture cannot inspect invocation state"))
			return
		}
		if value, err := invocation.Session().State().Get("interrupted_state"); err == nil || value != nil {
			yield(nil, errors.New("aborted state delta reached next invocation"))
			return
		}
		for _, content := range r.Contents {
			for _, part := range content.Parts {
				if part.FunctionCall != nil && part.FunctionCall.ID == "interrupted-call" {
					yield(nil, errors.New("unmatched function call reached model"))
					return
				}
			}
		}
		yield(&model.LLMResponse{Content: genai.NewContentFromText("state is clean", genai.RoleModel), TurnComplete: true}, nil)
	}
}
