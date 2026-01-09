package logging

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	logFormatOnce sync.Once
	logAsJSON     bool
)

func jsonEnabled() bool {
	logFormatOnce.Do(func() {
		val := strings.TrimSpace(os.Getenv("CORDUM_LOG_FORMAT"))
		switch strings.ToLower(val) {
		case "json":
			logAsJSON = true
			log.SetFlags(0)
		}
	})
	return logAsJSON
}

// Info logs a message with key/value fields using a consistent prefix.
func Info(component, msg string, kv ...interface{}) {
	if jsonEnabled() {
		logJSON("INFO", component, msg, kv...)
		return
	}
	log.Printf("[%s] %s%s", strings.ToUpper(component), msg, formatFields(kv...))
}

// Error logs an error message with key/value fields using a consistent prefix.
func Error(component, msg string, kv ...interface{}) {
	if jsonEnabled() {
		logJSON("ERROR", component, msg, kv...)
		return
	}
	log.Printf("[%s] ERROR %s%s", strings.ToUpper(component), msg, formatFields(kv...))
}

func logJSON(level, component, msg string, kv ...interface{}) {
	fields := map[string]any{
		"ts":        time.Now().UTC().Format(time.RFC3339Nano),
		"level":     level,
		"component": strings.TrimSpace(component),
		"msg":       msg,
	}
	if len(kv) > 0 {
		if len(kv)%2 != 0 {
			kv = append(kv, "(missing)")
		}
		extra := make(map[string]any, len(kv)/2)
		for i := 0; i < len(kv); i += 2 {
			key := strings.TrimSpace(toString(kv[i]))
			if key == "" {
				continue
			}
			extra[key] = kv[i+1]
		}
		if len(extra) > 0 {
			fields["fields"] = extra
		}
	}
	data, err := json.Marshal(fields)
	if err != nil {
		log.Printf("[%s] ERROR %s%s", strings.ToUpper(component), msg, formatFields(kv...))
		return
	}
	log.Print(string(data))
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
