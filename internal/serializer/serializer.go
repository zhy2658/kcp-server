package serializer

import (
	"fmt"

	gogoproto "github.com/gogo/protobuf/proto"
	protov1 "github.com/golang/protobuf/proto"
	protov2 "google.golang.org/protobuf/proto"
)

type Serializer struct{}

func NewSerializer() *Serializer {
	return &Serializer{}
}

func (s *Serializer) Marshal(v interface{}) ([]byte, error) {
	if v == nil {
		return []byte{}, nil
	}

	if pb, ok := v.(protov2.Message); ok {
		return protov2.Marshal(pb)
	}

	if pb, ok := v.(protov1.Message); ok {
		return protov1.Marshal(pb)
	}

	if pb, ok := v.(gogoproto.Message); ok {
		return gogoproto.Marshal(pb)
	}

	return nil, fmt.Errorf("protobuf: convert on wrong type value, got %T", v)
}

func (s *Serializer) Unmarshal(data []byte, v interface{}) error {
	if v == nil {
		return fmt.Errorf("protobuf: unmarshal expects non-nil target")
	}

	if pb, ok := v.(protov2.Message); ok {
		return protov2.Unmarshal(data, pb)
	}

	if pb, ok := v.(protov1.Message); ok {
		return protov1.Unmarshal(data, pb)
	}

	if pb, ok := v.(gogoproto.Message); ok {
		return gogoproto.Unmarshal(data, pb)
	}

	return fmt.Errorf("protobuf: convert on wrong type value, got %T", v)
}

func (s *Serializer) GetName() string {
	return "protobuf"
}
