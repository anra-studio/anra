package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	anramcp "github.com/anra-studio/anra/pkg/mcp"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/genai"
)

// The encoded audio exceeds both the former MCP result cap and the SDK's
// default request cap. Raw PCM silence is valid without a container header.
const mcpAudioBytes = (3 << 20) + 2

func (m *mcpScenario) largeAudioWindow() (*pb.TapeView, error) {
	state, err := m.tape.Get(m.tapeContext())
	if err != nil {
		return nil, err
	}
	start := state.Scope.SeqE.Next()
	content, err := json.Marshal(genai.Content{Role: "user", Parts: []*genai.Part{{
		InlineData: &genai.Blob{MIMEType: "audio/L16;rate=16000;channels=1", Data: make([]byte, mcpAudioBytes)},
	}}})
	if err != nil {
		return nil, err
	}
	if err := m.rawEvent(te.EntryUser, string(content)); err != nil {
		return nil, err
	}
	head, err := m.append(te.EntryAssistant, "excluded committed boundary")
	if err != nil {
		return nil, err
	}
	return &pb.TapeView{Source: m.agent.Source(), SnapshotHead: head.GetID().String(), Scope: &pb.EntryRange{SeqS: start.String(), SeqE: head.GetID().String()}}, nil
}

func (m *mcpScenario) readWithLargeBody() error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
		"name": anramcp.ReadTapeView, "arguments": map[string]string{"anchorSeq": m.grant.AnchorSeq},
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

func (m *mcpScenario) multimodalWindow() (*pb.TapeView, error) {
	state, err := m.tape.Get(m.tapeContext())
	if err != nil {
		return nil, err
	}
	start := state.Scope.SeqE.Next()
	if err := m.rawEvent(te.EntryUser, `{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U=","displayName":"receipt"},"partMetadata":{"private":"PRIVATE-METADATA"}},{"fileData":{"fileUri":"gs://context/invoice.pdf","mimeType":"application/pdf","displayName":"invoice"}}]}`); err != nil {
		return nil, err
	}
	if err := m.rawEvent(te.EntryAssistant, `{"role":"model","parts":[{"executableCode":{"id":"code-1","language":"PYTHON","code":"print(1)"},"thoughtSignature":"PRIVATE-SIGNATURE"},{"codeExecutionResult":{"id":"code-1","outcome":"OUTCOME_OK","output":"1\n"}},{"text":"The result is one."}]}`); err != nil {
		return nil, err
	}
	head, err := m.append(te.EntryAssistant, "excluded committed boundary")
	if err != nil {
		return nil, err
	}
	return &pb.TapeView{Source: m.agent.Source(), SnapshotHead: head.GetID().String(), Scope: &pb.EntryRange{SeqS: start.String(), SeqE: head.GetID().String()}}, nil
}

func (m *mcpScenario) checkMultimodal() error {
	if err := m.checkWindowResponse(); err != nil {
		return err
	}
	var window mcpWindow
	if err := json.Unmarshal(m.lastResult, &window); err != nil {
		return err
	}
	if len(window.Entries) != 2 {
		return fmt.Errorf("multimodal projection returned %d entries, want 2", len(window.Entries))
	}
	if bytes.Contains(m.lastResult, []byte("PRIVATE-")) || bytes.Contains(m.lastResult, []byte("thoughtSignature")) || bytes.Contains(m.lastResult, []byte("partMetadata")) {
		return errors.New("multimodal projection disclosed execution metadata")
	}
	// Decode with the SDK, independently of Projection's representation.
	var input, output genai.Content
	if err := json.Unmarshal(window.Entries[0].Content, &input); err != nil {
		return err
	}
	if err := json.Unmarshal(window.Entries[1].Content, &output); err != nil {
		return err
	}
	if input.Role != "user" || len(input.Parts) != 2 || output.Role != "model" || len(output.Parts) != 3 {
		return errors.New("multimodal projection lost content parts or their roles")
	}
	image, file := input.Parts[0].InlineData, input.Parts[1].FileData
	if image == nil || image.MIMEType != "image/png" || string(image.Data) != "image" || image.DisplayName != "receipt" ||
		file == nil || file.FileURI != "gs://context/invoice.pdf" || file.MIMEType != "application/pdf" || file.DisplayName != "invoice" {
		return errors.New("multimodal projection changed image/file content or order")
	}
	code, result := output.Parts[0].ExecutableCode, output.Parts[1].CodeExecutionResult
	if code == nil || code.ID != "code-1" || code.Language != genai.LanguagePython || code.Code != "print(1)" ||
		result == nil || result.ID != code.ID || result.Outcome != genai.OutcomeOK || result.Output != "1\n" || output.Parts[2].Text != "The result is one." {
		return errors.New("multimodal projection changed code content or order")
	}
	return nil
}
