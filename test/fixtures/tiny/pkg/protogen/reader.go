package protogen

// ReadPartitionID reads the proto field PartitionUpdate.partition_id; the
// proto_field_xref test asserts this access is found.
func ReadPartitionID(u *PartitionUpdate) uint64 {
	return u.PartitionId
}
