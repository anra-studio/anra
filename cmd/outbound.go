package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/scbizu/anra/pkg/agent"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/tape-go/pkg/runner"
	"github.com/spf13/viper"
)

const outboundPath = "/anra/send"

type outboundRequest struct {
	To      string `json:"to"`
	Message string `json:"message"`
}

type outboundResponse struct {
	Answer string `json:"answer"`
}

func outboundHandler(a *agent.Anra, settings *viper.Viper, authenticate func(*http.Request) (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := authenticate(r); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var value outboundRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&value); err != nil || !agentID.MatchString(value.To) || strings.TrimSpace(value.Message) == "" {
			http.Error(w, "a target agent and message are required", http.StatusBadRequest)
			return
		}
		token, err := peerToken(settings)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		client, err := discovery.NewClient(settings.GetString("registry"), token)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		record, err := client.Lookup(r.Context(), value.To)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		base, err := discovery.BaseURL(record)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		remote, err := newPeerRunner(r.Context(), base, settings)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if remote.(peerRunner).name != value.To {
			http.Error(w, "discovered card name mismatch", http.StatusBadGateway)
			return
		}
		answer, err := a.Exchange(r.Context(), value.Message, func(ctx context.Context, input string) (string, error) {
			response, err := remote.RunTurn(ctx, runner.Request{Messages: []runner.Message{{Role: runner.RoleUser, Text: input}}})
			return response.Output, err
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(outboundResponse{Answer: answer})
	})
}

// sendFromServer invokes the source agent's authenticated outbound endpoint.
// Its process owns the tape and resolves the destination through the directory.
func sendFromServer(ctx context.Context, source, target, message string, settings *viper.Viper) (string, error) {
	if !agentID.MatchString(target) {
		return "", errors.New("--to must be an agent name")
	}
	token, err := peerToken(settings)
	if err != nil {
		return "", err
	}
	directory, err := discovery.NewClient(settings.GetString("registry"), token)
	if err != nil {
		return "", err
	}
	record, err := directory.Lookup(ctx, source)
	if err != nil {
		return "", fmt.Errorf("source agent %q must be serving: %w", source, err)
	}
	base, err := discovery.BaseURL(record)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(outboundRequest{To: target, Message: message})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+outboundPath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	h := &http.Client{Transport: bearerTransport{token}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := h.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("source agent: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	var result outboundResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", err
	}
	if result.Answer == "" {
		return "", errors.New("source agent returned an empty answer")
	}
	return result.Answer, nil
}
