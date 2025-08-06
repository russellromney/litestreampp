package ultrasimple

import (
	"github.com/pierrec/lz4/v4"
)

// compressLZ4 compresses data using LZ4
func compressLZ4(data []byte) []byte {
	// Simple implementation - in production would handle errors
	maxSize := lz4.CompressBlockBound(len(data))
	compressed := make([]byte, maxSize)
	
	n, err := lz4.CompressBlock(data, compressed, nil)
	if err != nil {
		// Fallback to uncompressed
		return data
	}
	
	return compressed[:n]
}