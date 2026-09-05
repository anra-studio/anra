package lifecycle

import (
	"encoding/json"
	"testing"
)

func TestEntryValidation(t *testing.T) {
	tests := []struct {
		name    string
		entry   Entry
		wantErr bool
	}{
		{name: "valid", entry: Entry{Schema: SchemaV1, Kind: EventReceived, Data: []byte(`{"id":"e1","type":"input.text","payload":{}}`)}},
		{name: "unknown schema", entry: Entry{Schema: "anra.lifecycle/v2", Kind: EventReceived, Data: []byte(`{"id":"e1","type":"input.text","payload":{}}`)}, wantErr: true},
		{name: "unknown kind", entry: Entry{Schema: SchemaV1, Kind: "other", Data: []byte(`{}`)}, wantErr: true},
		{name: "invalid data", entry: Entry{Schema: SchemaV1, Kind: EventReceived, Data: []byte(`{`)}, wantErr: true},
		{name: "missing payload field", entry: Entry{Schema: SchemaV1, Kind: EventReceived, Data: []byte(`{"id":"e1","type":"input.text"}`)}, wantErr: true},
		{name: "unexpected correlation", entry: Entry{Schema: SchemaV1, Kind: EventReceived, ActivationID: "a1", Data: []byte(`{"id":"e1","type":"input.text","payload":{}}`)}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.entry.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNew(t *testing.T) {
	got, err := New(EventReceived, EventReceivedData{ID: "e1", Type: "input.text", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
