package gateway

const maxListLimit int64 = 500

func clampListLimit(limit int64) int64 {
	if limit <= 0 {
		return limit
	}
	if limit > maxListLimit {
		return maxListLimit
	}
	return limit
}
