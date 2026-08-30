package lifecycle

import "testing"

func TestEntryValidation(t *testing.T) {
	tests := []struct {
		name    string
		entry   Entry
		wantErr bool
	}{
		{name: "valid", entry: Entry{Schema: SchemaV1, Kind: EventReceived, Data: []byte(`{"id":"e1"}`)}},
		{name: "unknown schema", entry: Entry{Schema: "anra.lifecycle/v2", Kind: EventReceived, Data: []byte(`{}`)}, wantErr: true},
		{name: "unknown kind", entry: Entry{Schema: SchemaV1, Kind: "other", Data: []byte(`{}`)}, wantErr: true},
		{name: "invalid data", entry: Entry{Schema: SchemaV1, Kind: EventReceived, Data: []byte(`{`)}, wantErr: true},
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
	got, err := New(EventReceived, map[string]string{"id": "e1"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
