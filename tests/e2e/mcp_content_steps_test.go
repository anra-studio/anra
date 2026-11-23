package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	anramcp "github.com/anra-studio/anra/pkg/mcp"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/genai"
)

// The encoded audio exceeds both the former MCP result cap and the SDK's
// default request cap. Raw PCM silence is valid without a container header.
const mcpAudioBytes = (3 << 20) + 2

func (m *mcpScenario) largeAudioWindow() (*pb.Ref, error) {
	turn, err := beginFixtureTurn(m.s.ctx, m.viewStorage(), "vesta")
	if err != nil {
		return nil, err
	}

	content, err := json.Marshal(
		genai.Content{
			Role: "user",
			Parts: []*genai.Part{{
				InlineData: &genai.Blob{
					MIMEType: "audio/L16;rate=16000;channels=1",
					Data:     make([]byte, mcpAudioBytes),
				},
			}},
		},
	)
	if err != nil {
		return nil, err
	}
	if err := m.rawEvent(te.EntryUser, string(content)); err != nil {
		return nil, err
	}

	return finishFixtureTurn(m.s.ctx, m.viewStorage(), "vesta", turn)
}

func (m *mcpScenario) readWithLargeBody() error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
		"name": anramcp.ReadTapeView, "arguments": map[string]string{"anchorSeq": m.credential.AnchorSeq},
	}})
	if err != nil {
		return err
	}
	// Whitespace changes transport size without changing the authorized request.
	body = append(body, bytes.Repeat([]byte(" "), 5<<20)...)
	return m.sendRequest(body, m.token, nil)
}

func (m *mcpScenario) checkLargeAudio() error {
	if err := m.checkWindowResponse(); err != nil {
		return err
	}
	var window mcpWindow
	if err := json.Unmarshal(m.lastResult, &window); err != nil {
		return err
	}
	if len(window.Entries) != 1 {
		return fmt.Errorf("audio view has %d entries, want 1", len(window.Entries))
	}
	var content genai.Content
	if err := json.Unmarshal(window.Entries[0].Content, &content); err != nil {
		return err
	}
	if content.Role != "user" || len(content.Parts) != 1 || content.Parts[0].InlineData == nil {
		return errors.New("audio view lost its role or inline data")
	}
	audio := content.Parts[0].InlineData
	if audio.MIMEType != "audio/L16;rate=16000;channels=1" || !bytes.Equal(audio.Data, make([]byte, mcpAudioBytes)) {
		return errors.New("audio view changed or truncated the audio")
	}
	return nil
}
