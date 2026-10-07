package tapestorage

import (
	"path/filepath"
	"testing"

	"github.com/scbizu/tape-go/pkg/tape/storage"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	"github.com/scbizu/tape-go/pkg/tape/storage/jsonl"
)

type decorator struct{ storage.TapeStorage }

func (d *decorator) Unwrap() storage.TapeStorage { return d.TapeStorage }

func TestRequireBbolt(t *testing.T) {
	boltTape, err := bbolt.NewBboltStorage("session", filepath.Join(t.TempDir(), "tape.db"))
	if err != nil {
		t.Fatal(err)
	}
	jsonTape, err := jsonl.NewJSONLStorage("session", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var nilBolt *bbolt.Bbolt
	var nilWrapper *decorator
	cycle := &decorator{}
	cycle.TapeStorage = cycle
	for _, tc := range []struct {
		name  string
		tape  storage.TapeStorage
		valid bool
	}{
		{"bbolt", boltTape, true},
		{"wrapped-bbolt", &decorator{&decorator{boltTape}}, true},
		{"jsonl", jsonTape, false},
		{"wrapped-jsonl", &decorator{jsonTape}, false},
		{"nil", nil, false},
		{"typed-nil-bbolt", nilBolt, false},
		{"typed-nil-wrapper", nilWrapper, false},
		{"wrapped-nil", &decorator{nilBolt}, false},
		{"cycle", cycle, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := RequireBbolt(tc.tape); (err == nil) != tc.valid {
				t.Fatalf("RequireBbolt() = %v, valid = %v", err, tc.valid)
			}
		})
	}
}
