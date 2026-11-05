package entry_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/genai"
)

func eventEntry(content string) entry.Entry {
	return entry.FromTape(te.NewEntry(te.WithEntryKind(te.EntryAssistant), te.WithEntryContent("PRIVATE-CONTENT"))).
		WithExtension(entry.ADKEvent, `{"content":`+content+`,"stateDelta":{"secret":"PRIVATE-STATE"},"author":"PRIVATE-AUTHOR"}`).
		WithExtension("secret", "PRIVATE-EXTENSION")
}

func TestStoredExtraFieldsDoNotExpandPublicDisclosure(t *testing.T) {
	raw := eventEntry(`{"role":"model","extra":"PRIVATE-CONTENT","channel":"primary","parts":[{"text":"public","extra":"PRIVATE-PART"},{"functionCall":{"name":"lookup","args":{},"extra":"PRIVATE-CALL"}},{"functionResponse":{"name":"lookup","response":{},"willContinue":false,"scheduling":"WHEN_IDLE","extra":"PRIVATE-RESPONSE","parts":[{"inlineData":{"data":"aW1hZ2U=","mimeType":"image/png","displayName":"receipt","extra":"PRIVATE-MEDIA"}},{"fileData":{"fileUri":"gs://context/receipt","mimeType":"application/pdf","displayName":"invoice","extra":"PRIVATE-FILE"}}]}}]}`)
	got, err := raw.Content()
	if err != nil {
		t.Fatal(err)
	}
	// SDK-backed parts may also be assembled in memory. Encoding must exclude
	// metadata even when it did not pass through the storage decoder.
	got.Parts[0].ThoughtSignature = []byte("PRIVATE-SIGNATURE")
	got.Parts[0].PartMetadata = map[string]any{"private": "PRIVATE-METADATA"}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("PRIVATE-")) {
		t.Fatalf("stored fields expanded public content: %s", data)
	}
	if len(got.Parts) != 3 || got.Parts[1].FunctionCall.Name != "lookup" {
		t.Fatalf("public content lost during content encoding: %s", data)
	}
	response := got.Parts[2].FunctionResponse
	if len(response.Parts) != 2 || response.WillContinue == nil || *response.WillContinue || response.Scheduling != genai.FunctionResponseSchedulingWhenIdle {
		t.Fatalf("tool response lost media or scheduling: %s", data)
	}
	inline, file := response.Parts[0].InlineData, response.Parts[1].FileData
	if inline == nil || string(inline.Data) != "image" || inline.MIMEType != "image/png" || inline.DisplayName != "receipt" ||
		file == nil || file.FileURI != "gs://context/receipt" || file.MIMEType != "application/pdf" || file.DisplayName != "invoice" {
		t.Fatalf("SDK media changed during content encoding: %s", data)
	}
}

func TestUnreadableContentCannotBeDecoded(t *testing.T) {
	raw := eventEntry(`{`)
	got, err := raw.Content()
	if !errors.Is(err, entry.ErrUnsupported) || got != nil {
		t.Fatalf("Content() = %#v, %v; want nil, ErrUnsupported", got, err)
	}
}

func TestBusinessJSONSurvivesContentEncoding(t *testing.T) {
	for _, business := range []string{
		`{"id":9007199254740993,"fraction":0.12345678901234567890123456789,"tiny":1e-1000,"futureVisiblePart":{"text":"business data","id":9007199254740993},"thoughtSignature":"business data","partMetadata":{"value":"business data"}}`,
		`{}`,
	} {
		raw := eventEntry(`{"parts":[{"functionCall":{"name":"lookup","args":` + business + `}},{"functionResponse":{"name":"lookup","response":` + business + `}},{"toolCall":{"id":"search-1","toolType":"GOOGLE_SEARCH_WEB","args":` + business + `}},{"toolResponse":{"id":"search-1","toolType":"GOOGLE_SEARCH_WEB","response":` + business + `}}]}`)
		got, err := raw.Content()
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip entry.Content
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatal(err)
		}
		if len(roundTrip.Parts) != 4 || roundTrip.Parts[0].FunctionCall == nil || roundTrip.Parts[1].FunctionResponse == nil || roundTrip.Parts[2].ToolCall == nil || roundTrip.Parts[3].ToolResponse == nil {
			t.Fatalf("lost tool content: %s", encoded)
		}
		for _, data := range []json.RawMessage{roundTrip.Parts[0].FunctionCall.Args, roundTrip.Parts[1].FunctionResponse.Response, roundTrip.Parts[2].ToolCall.Args, roundTrip.Parts[3].ToolResponse.Response} {
			if string(data) != business {
				t.Fatalf("business JSON changed: got %s, want %s", data, business)
			}
		}
	}
}

func TestThoughtContentRetainsMarkersAndOmitsExecutionMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, parts, encoded string
	}{
		{"mixed", `[{"text":"Shared reasoning","thought":true,"futurePrivate":true},{"text":"public","thoughtSignature":"PRIVATE-SIGNATURE"}]`, `{"role":"model","parts":[{"text":"Shared reasoning","thought":true},{"text":"public"}]}`},
		{"thought only", `[{"text":"Shared reasoning","thought":true,"thoughtSignature":"PRIVATE-SIGNATURE"}]`, `{"role":"model","parts":[{"text":"Shared reasoning","thought":true}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := eventEntry(`{"role":"model","parts":` + tc.parts + `}`).Content()
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.encoded {
				t.Fatalf("unexpected public content: %s", encoded)
			}
		})
	}
}
