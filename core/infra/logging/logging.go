package logging

import (
	"fmt"
	"log"
	"strings"
)

// Info logs a message with key/value fields using a consistent prefix.
func Info(component, msg string, kv ...interface{}) {
	log.Printf("[%s] %s%s", strings.ToUpper(component), msg, formatFields(kv...))
}

// Error logs an error message with key/value fields using a consistent prefix.
func Error(component, msg string, kv ...interface{}) {
	log.Printf("[%s] ERROR %s%s", strings.ToUpper(component), msg, formatFields(kv...))
}

func formatFields(kv ...interface{}) string {
	if len(kv) == 0 {
		return ""
	}
	if len(kv)%2 != 0 {
		kv = append(kv, "(missing)")
	}
	var b strings.Builder
	b.WriteString(" ")
	for i := 0; i < len(kv); i += 2 {
		if i > 0 {
			b.WriteString(" ")
		}
		key := kv[i]
		val := kv[i+1]
		b.WriteString(strings.TrimSpace(toString(key)))
		b.WriteString("=")
		b.WriteString(toString(val))
	}
	return b.String()
}

func toString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(fmt.Sprintf("%v", t)), "\n", " "), "\t", " "))
	}
}
