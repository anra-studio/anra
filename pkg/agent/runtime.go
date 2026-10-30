package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/anra/pkg/model/delegator"
	tapeagent "github.com/scbizu/tape-go/pkg/agent"
	"github.com/scbizu/tape-go/pkg/tape"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/view"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/genai"
)

const defaultMaxModelTurns = 32
const appName = "anra"

var (
	ErrRuntimeNotConfigured  = errors.New("agent: runtime is not configured")
	ErrRuntimeModelTurnLimit = errors.New("agent: runtime model turn limit reached")
)

type runtimeConfig struct {
	llm               model.LLM
	systemInstruction string
	registry          *discovery.Client
	peerHTTP          *http.Client
	borrowClient      *a2a.Client
	maxModelTurns     int
	timeout           time.Duration
	adapter           *tapeagent.TapeAdapter
	runner            *adkrunner.Runner
	sessionID         string
}

type Option func(*Anra) error

func WithLLM(value model.LLM) Option {
	return func(a *Anra) error {
		if value == nil {
			return errors.New("agent: nil model")
		}
		a.runtime.llm = value
		return nil
	}
}

func WithSystemInstruction(value string) Option {
	return func(a *Anra) error { a.runtime.systemInstruction = value; return nil }
}

// WithMaxModelTurns bounds model interactions within one Agent Turn.
func WithMaxModelTurns(limit int) Option {
	return func(a *Anra) error {
		if limit <= 0 {
			return errors.New("agent: max model turns must be positive")
		}
		a.runtime.maxModelTurns = limit
		return nil
	}
}

// WithTimeout caps each Agent Turn, including its peer calls. Cleanup uses a
// separate bounded context so cancellation cannot strand an open start.
func WithTimeout(limit time.Duration) Option {
	return func(a *Anra) error {
		if limit <= 0 {
			return errors.New("agent: timeout must be positive")
		}
		a.runtime.timeout = limit
		return nil
	}
}

type modelTurnCountKey struct{}

func (a *Anra) initialize() error {
	ctx := owner.WithOwnerId(context.Background(), a.principal)
	state, err := a.tape.Get(ctx)
	if err != nil {
		return err
	}
	a.runtime.sessionID = state.SessionID
	wrapped := &eventStorage{TapeStorage: a.tape, aborted: append([]view.EntryRange(nil), a.execution.aborted...)}
	adapter, err := tapeagent.NewTapeAdapter(&tape.Tape{TapeStorage: wrapped, OwnerID: a.principal}, appName)
	if err != nil {
		return err
	}
	a.runtime.adapter = adapter
	if a.runtime.llm == nil {
		return nil
	}
	tools, err := a.builtinTools()
	if err != nil {
		return err
	}
	runtime, err := tapeagent.NewRuntime(llmagent.Config{
		Name:                appName,
		Description:         "Tape-backed Anra agent",
		Model:               a.runtime.llm,
		InstructionProvider: func(adkagent.ReadonlyContext) (string, error) { return a.runtime.systemInstruction, nil },
		Tools:               tools,
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{
			func(ctx adkagent.CallbackContext, _ *model.LLMRequest) (*model.LLMResponse, error) {
				count, ok := ctx.Value(modelTurnCountKey{}).(*int)
				if !ok {
					return nil, errors.New("agent: missing model turn counter")
				}
				if *count >= a.runtime.maxModelTurns {
					return nil, ErrRuntimeModelTurnLimit
				}
				(*count)++
				return nil, nil
			},
			adapter.ContextWindow,
		},
	})
	if err != nil {
		return err
	}
	a.runtime.runner, err = adkrunner.New(adkrunner.Config{
		AppName: appName, Agent: runtime, SessionService: adapter, MemoryService: adapter,
	})
	return err
}

// Run admits one Agent Turn and delegates the model/tool loop and event
// persistence to ADK and tape-go.
func (a *Anra) Run(ctx context.Context, input string) (answer string, runErr error) {
	if a.runtime.llm == nil {
		return "", ErrRuntimeNotConfigured
	}
	if strings.TrimSpace(input) == "" {
		return "", errors.New("agent: empty runtime input")
	}
	ctx, runCancel := context.WithTimeout(ctx, a.runtime.timeout)
	defer runCancel()
	ctx, cancel := delegator.Begin(ctx)
	defer cancel()
	done, err := a.admit(ctx, cancel)
	if err != nil {
		return "", err
	}
	defer func() {
		defer close(done)
		if err := a.finish(runErr); err != nil {
			answer = ""
			runErr = errors.Join(runErr, err)
		}
	}()
	count := 0
	ctx = context.WithValue(ctx, modelTurnCountKey{}, &count)

	for event, err := range a.runtime.runner.Run(ctx, a.principal, a.runtime.sessionID, genai.NewContentFromText(input, genai.RoleUser), adkagent.RunConfig{}) {
		if err != nil {
			return "", fmt.Errorf("agent: ADK run: %w", err)
		}
		if event.ErrorCode != "" || event.ErrorMessage != "" {
			return "", fmt.Errorf("agent: model error %s: %s", event.ErrorCode, event.ErrorMessage)
		}
		if event.IsFinalResponse() && event.Content != nil {
			var text strings.Builder
			for _, part := range event.Content.Parts {
				if part != nil && !part.Thought {
					text.WriteString(part.Text)
				}
			}
			if text.Len() > 0 {
				answer = text.String()
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if answer == "" {
		return "", errors.New("agent: ADK run returned no answer")
	}
	return answer, nil
}
