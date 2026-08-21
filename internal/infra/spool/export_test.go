package spool

// SetMaxSpillBytes sets the disk spill threshold in bytes; 0 or negative means never spill.
func SetMaxSpillBytes(limit int64) {
	maxMemSize = limit
}
