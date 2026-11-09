package borrow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/iotest"

	"github.com/anra-studio/anra/pkg/model/view"
)

func TestEntryStreamPreservesRecordsAcrossSmallReads(t *testing.T) {
	t.Parallel()
	source := strings.NewReader(`{"Seq":"3","Ek":"user","Text":"first","Owner":"owner"}
{"Seq":"9","Ek":"assistant","Text":"second","Owner":"owner"}`)
	entries := &entryStream{
		decoder:  json.NewDecoder(source),
		exporter: view.TextExporter{},
	}
	stream := io.MultiReader(strings.NewReader("["), entries, strings.NewReader("]"))
	data, err := io.ReadAll(iotest.OneByteReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	var result []view.Entry
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].Seq != "3" || result[1].Seq != "9" || string(result[0].Content) != `"first"` || string(result[1].Content) != `"second"` {
		t.Fatalf("stream changed entry order, framing or content: %s", data)
	}
}

func TestReadStreamFailureDoesNotBecomeNormalEOF(t *testing.T) {
	t.Parallel()
	failure := errors.New("source failed")
	entries := &entryStream{
		decoder: json.NewDecoder(io.MultiReader(
			strings.NewReader(`{"Seq":"3","Ek":"user","Text":"first","Owner":"owner"}`),
			iotest.ErrReader(failure),
		)),
		exporter: view.TextExporter{},
	}
	stream := &readStream{
		ctx:    context.Background(),
		mu:     &sync.Mutex{},
		source: io.MultiReader(strings.NewReader("["), entries, strings.NewReader("]")),
	}
	data, err := io.ReadAll(stream)
	if !errors.Is(err, failure) || !errors.Is(err, ErrStorage) || strings.HasSuffix(string(data), "]") {
		t.Fatalf("source failure produced a complete result: %s, error %v", data, err)
	}
	if n, err := stream.Read(make([]byte, 1)); n != 0 || !errors.Is(err, failure) {
		t.Fatalf("failed reader resumed: %d, %v", n, err)
	}
}
