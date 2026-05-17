// Package protogen mimics the shape of protoc-gen-go output: structs with
// `protobuf:"...,name=..."` tags. The lint/proto_field_xref tools key off the
// tag, so no real protobuf dependency is needed.
package protogen

// PartitionUpdate mirrors what protoc-gen-go would emit.
type PartitionUpdate struct {
	PartitionId uint64 `protobuf:"varint,1,opt,name=partition_id,proto3" json:"partition_id,omitempty"`
	Payload     []byte `protobuf:"bytes,2,opt,name=payload,proto3" json:"payload,omitempty"`
}
