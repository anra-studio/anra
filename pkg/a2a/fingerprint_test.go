package a2a

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

func fingerprintFixture(n int) []storedEntry {
	entries := make([]storedEntry, n)
	for i := range entries {
		entries[i] = storedEntry{
			ProjectedEntry: tv.ProjectedEntry{Seq: te.SeqFromUint64(uint64(i + 1)), Kind: te.EntryAssistant, Summary: strings.Repeat("finding <>&\n", 32)},
			Timestamp:      time.Date(2026, 10, 11, 0, 0, i, 1234, time.UTC),
			Payload:        &storedPayload{MediaType: "application/json", Data: json.RawMessage(` { "text": "中<>&", "value": 1 } `)},
		}
	}
	return entries
}

func TestEntryFingerprintMatchesPersistedJSONFormat(t *testing.T) {
	for _, n := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			entries := fingerprintFixture(n)
			if n > 1 {
				entries[1].Payload = nil
			}
			encoded, err := json.Marshal(entries)
			if err != nil {
				t.Fatal(err)
			}
			f := newEntryFingerprint()
			for _, e := range entries {
				b, err := json.Marshal(e)
				if err != nil {
					t.Fatal(err)
				}
				f.add(b)
			}
			if got, want := f.sum(), fingerprint(encoded); got != want {
				t.Fatalf("fingerprint changed: %s, want %s", got, want)
			}
		})
	}
}

var fingerprintResult string

func BenchmarkEntryFingerprint(b *testing.B) {
	for _, n := range []int{32, 1000} {
		entries := fingerprintFixture(n)
		encoded := make([][]byte, n)
		for i, e := range entries {
			var err error
			encoded[i], err = json.Marshal(e)
			if err != nil {
				b.Fatal(err)
			}
		}
		b.Run(fmt.Sprintf("%d/serialize-again", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				data, err := json.Marshal(entries)
				if err != nil {
					b.Fatal(err)
				}
				fingerprintResult = fingerprint(data)
			}
		})
		b.Run(fmt.Sprintf("%d/reuse-entry-bytes", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				f := newEntryFingerprint()
				for _, data := range encoded {
					f.add(data)
				}
				fingerprintResult = f.sum()
			}
		})
	}
}
