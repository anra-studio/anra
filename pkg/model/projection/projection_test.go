package projection_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/anra-studio/anra/pkg/model/projection"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

func eventEntry(content string) te.CustomEntry {
	return te.CustomEntry{
		Entry: te.NewEntry(te.WithEntryKind(te.EntryAssistant), te.WithEntryContent("PRIVATE-SUMMARY")),
		Extensions: map[string]any{
			"adk:event": `{"content":` + content + `,"stateDelta":{"secret":"PRIVATE-STATE"},"author":"PRIVATE-AUTHOR"}`,
			"secret":    "PRIVATE-EXTENSION",
		},
	}
}

func TestUnknownProtocolFieldsRejectWholeEntry(t *testing.T) {
	// Each case crosses a distinct protocol object boundary. Business JSON is
	// deliberately excluded: its keys are opaque, as checked separately below.
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"content", `{"parts":[{"text":"visible"}],"futureContent":true}`},
		{"part", `{"parts":[{"text":"visible"},{"text":"suffix","futurePart":true}]}`},
		{"call", `{"parts":[{"functionCall":{"name":"lookup","futureCall":true}}]}`},
		{"streamed arguments", `{"parts":[{"functionCall":{"name":"lookup","partialArgs":[{"jsonPath":"$.query","stringValue":"partial"}]}}]}`},
		{"call continuation", `{"parts":[{"functionCall":{"name":"lookup","willContinue":true}}]}`},
		{"response", `{"parts":[{"functionResponse":{"name":"lookup","futureResponse":true}}]}`},
		{"response part", `{"parts":[{"functionResponse":{"name":"lookup","parts":[{"futurePart":true}]}}]}`},
		{"inline media", `{"parts":[{"functionResponse":{"name":"lookup","parts":[{"inlineData":{"data":"aW1hZ2U=","futureMedia":true}}]}}]}`},
		{"file media", `{"parts":[{"functionResponse":{"name":"lookup","parts":[{"fileData":{"fileUri":"gs://image","futureMedia":true}}]}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Unsupported fields must not produce a partial successful projection.
			raw := eventEntry(tc.content)
			got, err := projection.Project(raw)
			if !errors.Is(err, projection.ErrUnsupported) || got != nil {
				t.Fatalf("Project() = %#v, %v; want nil, ErrUnsupported", got, err)
			}
		})
	}
}

func TestBusinessJSONSurvivesProjectionAndEncoding(t *testing.T) {
	for _, business := range []string{
		`{"id":9007199254740993,"fraction":0.12345678901234567890123456789,"tiny":1e-1000,"futureVisiblePart":{"text":"business data","id":9007199254740993}}`,
		`{}`,
	} {
		raw := eventEntry(`{"parts":[{"functionCall":{"name":"lookup","args":` + business + `}},{"functionResponse":{"name":"lookup","response":` + business + `}}]}`)
		got, err := projection.Project(raw)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(got.Event)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip projection.Event
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatal(err)
		}
		if len(roundTrip.Parts) != 2 || roundTrip.Parts[0].FunctionCall == nil || roundTrip.Parts[1].FunctionResponse == nil {
			t.Fatalf("lost tool content: %s", encoded)
		}
		for _, data := range []json.RawMessage{roundTrip.Parts[0].FunctionCall.Args, roundTrip.Parts[1].FunctionResponse.Response} {
			if string(data) != business {
				t.Fatalf("business JSON changed: got %s, want %s", data, business)
			}
		}
	}
}

func TestPrivateThoughtsAndMetadataAreOmitted(t *testing.T) {
	for _, tc := range []struct {
		name, parts, summary, encoded string
	}{
		{"mixed", `[{"text":"PRIVATE-THOUGHT","thought":true,"futurePrivate":true},{"text":"public","thoughtSignature":"PRIVATE-SIGNATURE"}]`, "public", `{"role":"model","parts":[{"text":"public"}]}`},
		{"thought only", `[{"text":"PRIVATE-THOUGHT","thought":true}]`, "", `{"role":"model"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := projection.Project(eventEntry(`{"role":"model","parts":` + tc.parts + `}`))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(got.Event)
			if err != nil {
				t.Fatal(err)
			}
			if got.Summary != tc.summary || string(encoded) != tc.encoded {
				t.Fatalf("unexpected public projection: summary %q, event %s", got.Summary, encoded)
			}
		})
	}
}
