package gateway

import (
	"fmt"
	"strings"
)

func trimStringSlice(values []string) []string {
	if len(values) == 0 {
		return values
	}
	trimmed := make([]string, len(values))
	for i, value := range values {
		trimmed[i] = strings.TrimSpace(value)
	}
	return trimmed
}

func validateStringArray(field string, values []string, maxItems, maxLen int) error {
	if len(values) > maxItems {
		return fmt.Errorf("%s must contain at most %d items", field, maxItems)
	}
	for i, value := range values {
		if len(value) > maxLen {
			return fmt.Errorf("%s[%d] must be at most %d characters", field, i, maxLen)
		}
	}
	return nil
}
