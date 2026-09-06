package handler

import (
	"fmt"

	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type storageByteCapacity struct {
	UsedBytes  int64
	FreeBytes  int64
	TotalBytes int64
	HasData    bool
}

func storageCapacityFromHealth(resp *healthv1.HealthCheckResponse) storageByteCapacity {
	if resp == nil {
		return storageByteCapacity{}
	}
	return storageCapacityFromLoad(resp.GetLoad())
}

func storageCapacityFromLoad(load map[string]float64) storageByteCapacity {
	if len(load) == 0 {
		return storageByteCapacity{}
	}
	used := loadInt64(load, "used_bytes")
	free := loadInt64(load, "free_bytes")
	total := loadInt64(load, "total_bytes")
	if total <= 0 && used > 0 && free > 0 {
		total = used + free
	}
	if total <= 0 && free > 0 {
		total = free + used
	}
	has := used > 0 || free > 0 || total > 0
	return storageByteCapacity{
		UsedBytes:  used,
		FreeBytes:  free,
		TotalBytes: total,
		HasData:    has,
	}
}

func loadInt64(load map[string]float64, key string) int64 {
	if v, ok := load[key]; ok && v > 0 {
		return int64(v)
	}
	return 0
}

func storageCapacityFromProto(msg protoreflect.Message) storageByteCapacity {
	if !msg.IsValid() {
		return storageByteCapacity{}
	}
	used := protoInt64Field(msg, "used_bytes")
	free := protoInt64Field(msg, "free_bytes")
	total := protoInt64Field(msg, "total_bytes")
	if total <= 0 && used > 0 && free > 0 {
		total = used + free
	}
	has := used > 0 || free > 0 || total > 0
	return storageByteCapacity{
		UsedBytes:  used,
		FreeBytes:  free,
		TotalBytes: total,
		HasData:    has,
	}
}

func protoInt64Field(msg protoreflect.Message, name protoreflect.Name) int64 {
	fd := msg.Descriptor().Fields().ByName(name)
	if fd == nil || !msg.Has(fd) {
		return 0
	}
	return msg.Get(fd).Int()
}

func protoStringField(msg protoreflect.Message, name protoreflect.Name) string {
	fd := msg.Descriptor().Fields().ByName(name)
	if fd == nil || !msg.Has(fd) {
		return ""
	}
	return msg.Get(fd).String()
}

func storageProviderCapacitiesFromProto(msg protoreflect.Message) map[string]storageByteCapacity {
	out := map[string]storageByteCapacity{}
	if !msg.IsValid() {
		return out
	}
	fd := msg.Descriptor().Fields().ByName("providers")
	if fd == nil {
		return out
	}
	list := msg.Get(fd).List()
	for i := 0; i < list.Len(); i++ {
		item := list.Get(i).Message()
		cap := storageCapacityFromProto(item)
		if !cap.HasData {
			continue
		}
		id := protoStringField(item, "provider_id")
		if id == "" {
			id = protoStringField(item, "id")
		}
		if id == "" {
			id = protoStringField(item, "module_id")
		}
		if id == "" {
			continue
		}
		out[id] = cap
	}
	return out
}

func formatStorageCapacityBytes(n int64) string {
	if n <= 0 {
		return "—"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for cur := n / unit; cur >= unit; cur /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
