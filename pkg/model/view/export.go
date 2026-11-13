package view

import (
	"encoding/json"
	"errors"

	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

const (
	TextMediaType    = "text/plain"
	ContextMediaType = "application/vnd.anra.context-entry+json;version=1"
	AnchorMediaType  = "application/vnd.anra.anchor+json;version=1"
)

var ErrUnsupported = errors.New("unsupported entry representation")

type ContextExporter struct{}

func (ContextExporter) Export(raw entry.Entry) (Entry, error) {
	e := Entry{
		Seq:       raw.GetID().String(),
		Kind:      string(raw.GetKind()),
		Timestamp: raw.GetTimestamp(),
		MediaType: ContextMediaType,
	}
	var body any
	var err error
	if raw.GetKind() == anchor.Kind {
		body, err = anchor.Export(raw)
		e.MediaType = AnchorMediaType
		if errors.Is(err, anchor.ErrUnsupported) {
			// Keep the entry's sequence but do not expose unknown anchor content.
			body, err = nil, nil
		}
	} else {
		body, err = raw.Content()
	}
	if err != nil {
		return Entry{}, ErrUnsupported
	}
	e.Content, err = json.Marshal(body)
	if err != nil {
		return Entry{}, ErrUnsupported
	}
	return e, nil
}

type TextExporter struct{}

func (TextExporter) Export(raw entry.Entry) (Entry, error) {
	if raw.GetKind() != te.EntryUser && raw.GetKind() != te.EntryAssistant {
		return Entry{}, ErrUnsupported
	}
	data, err := json.Marshal(raw.Body())
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		Seq:       raw.GetID().String(),
		Kind:      string(raw.GetKind()),
		Timestamp: raw.GetTimestamp(),
		MediaType: TextMediaType,
		Content:   data,
	}, nil
}
