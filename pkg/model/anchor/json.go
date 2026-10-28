package anchor

import (
	"bytes"
	"encoding/json"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ProtoJSON preserves protobuf's public camelCase representation inside custom
// JSON records. Internal references remain generated protobuf messages.
func ProtoJSON(m proto.Message) json.RawMessage {
	if m == nil || !m.ProtoReflect().IsValid() {
		return json.RawMessage("null")
	}
	data, _ := protojson.Marshal(m)
	return data
}
func (p Publication) MarshalJSON() ([]byte, error) {
	ranges := make([]json.RawMessage, len(p.Covers))
	for i, r := range p.Covers {
		ranges[i] = ProtoJSON(r)
	}
	return json.Marshal(struct {
		Summary    string            `json:"summary"`
		Covers     []json.RawMessage `json:"covers"`
		SourceHead string            `json:"sourceHead"`
	}{p.Summary, ranges, p.SourceHead})
}
func (p *Publication) UnmarshalJSON(data []byte) error {
	var wire struct {
		Summary    string            `json:"summary"`
		Covers     []json.RawMessage `json:"covers"`
		SourceHead string            `json:"sourceHead"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	p.Summary, p.SourceHead = wire.Summary, wire.SourceHead
	for _, raw := range wire.Covers {
		r := new(pb.EntryRange)
		if err := protojson.Unmarshal(raw, r); err != nil {
			return err
		}
		p.Covers = append(p.Covers, r)
	}
	return nil
}
func (r Record) MarshalJSON() ([]byte, error) {
	type record Record
	data, err := json.Marshal(record(r))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	if r.Source != nil {
		fields["source"] = ProtoJSON(r.Source)
	}
	if r.BasePublication != nil {
		fields["basePublication"] = ProtoJSON(r.BasePublication)
	}
	return json.Marshal(fields)
}
func (r *Record) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	source, base := fields["source"], fields["basePublication"]
	delete(fields, "source")
	delete(fields, "basePublication")
	rest, _ := json.Marshal(fields)
	type record Record
	decoder := json.NewDecoder(bytes.NewReader(rest))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode((*record)(r)); err != nil {
		return err
	}
	if len(source) > 0 && string(source) != "null" {
		r.Source = new(pb.Source)
		if err := protojson.Unmarshal(source, r.Source); err != nil {
			return err
		}
	}
	if len(base) > 0 && string(base) != "null" {
		r.BasePublication = new(pb.AnchorRef)
		if err := protojson.Unmarshal(base, r.BasePublication); err != nil {
			return err
		}
	}
	return nil
}
