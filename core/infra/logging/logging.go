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

// Warn logs a warning message with key/value fields using a consistent prefix.
func Warn(component, msg string, kv ...interface{}) {
	if jsonEnabled() {
		logJSON("WARN", component, msg, kv...)
		return
	}
	log.Printf("[%s] WARN %s%s", strings.ToUpper(component), msg, formatFields(kv...))
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
			extra[key] = redactValue(key, kv[i+1])
		}
		if len(extra) > 0 {
			fields["fields"] = extra
		}
	}
	data, err := json.Marshal(fields)
	if err != nil {
		log.Printf("[%s] ERROR marshal failed", strings.ToUpper(component))
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
		key := strings.TrimSpace(toString(kv[i]))
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(toString(redactValue(key, kv[i+1])))
	}
	return b.String()
}

// sensitiveKey returns true if the key name suggests a secret or credential.
func sensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, s := range []string{"password", "passwd", "secret", "token", "api_key", "apikey", "credential", "auth"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// redactValue replaces the value with [REDACTED] if the key is sensitive.
func redactValue(key string, val interface{}) interface{} {
	if sensitiveKey(key) {
		return "[REDACTED]"
	}
	return val
}

func toString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(fmt.Sprintf("%v", t)), "\n", " "), "\t", " "))
	}
}
