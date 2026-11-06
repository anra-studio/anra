package view

import (
	"encoding/json"

	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

const (
	TextMediaType    = "text/plain"
	ContextMediaType = "application/vnd.anra.context-entry+json;version=1"
	AnchorMediaType  = "application/vnd.anra.anchor+json;version=1"
)

type (
	Exporter interface {
		Export(te.EntryLike) (Entry, error)
	}
	ContextExporter struct{}
)

func (ContextExporter) Export(raw te.EntryLike) (Entry, error) {
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
	} else {
		body, err = entry.FromTape(raw).Content()
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

func (TextExporter) Export(raw te.EntryLike) (Entry, error) {
	if raw.GetKind() != te.EntryUser && raw.GetKind() != te.EntryAssistant {
		return Entry{}, ErrUnsupported
	}
	data, err := json.Marshal(entry.FromTape(raw).Body())
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
