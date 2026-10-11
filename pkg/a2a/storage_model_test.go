package a2a

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestExistingGrantJournalFormat(t *testing.T) {
	// Persisted before the public API switched to generated protobuf messages.
	legacy := []byte(`{"schema":"anra:tape-view-grant/v1","source":{"agentId":"peer","tapeId":"tape"},"requester":"borrower","request":{"requestId":"request","purpose":"research","query":"storage","bounds":{"SeqS":"18446744073709551616","SeqE":"18446744073709551617"}},"decision":{"requestId":"request","status":"granted","view":{"viewId":"view","source":{"agentId":"peer","tapeId":"tape"},"snapshotHead":"18446744073709551616","ranges":[{"SeqS":"18446744073709551616","SeqE":"18446744073709551617"}],"expiresAt":"2026-10-11T01:00:00Z"}},"session":"session","headHash":"head","entryHash":"entries","codec":"anra:text-summary/v1"}`)
	var record grantRecord
	if err := json.Unmarshal(legacy, &record); err != nil {
		t.Fatal(err)
	}
	if err := validateDecision(record.Decision, record.Request.RequestId); err != nil {
		t.Fatal(err)
	}
	if record.Request.Bounds.SeqS != "18446744073709551616" || !proto.Equal(record.Source, record.Decision.View.Source) {
		t.Fatal("lost source or sequence")
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(legacy) {
		t.Fatalf("changed persisted format:\n%s", data)
	}
}
