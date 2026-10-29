package anchor

import (
	"bytes"
	"encoding/json"
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
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
	if r.BaseView != nil {
		fields["baseView"] = ProtoJSON(r.BaseView)
	}
	return json.Marshal(fields)
}
func (r *Record) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	source, base := fields["source"], fields["baseView"]
	delete(fields, "source")
	delete(fields, "baseView")
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
		r.BaseView = new(pb.TapeView)
		if err := protojson.Unmarshal(base, r.BaseView); err != nil {
			return err
		}
	}
	return nil
}
