package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type lokiWriteSyncer struct {
	pushURL string
	client  *http.Client
	labels  map[string]string
	mu      sync.Mutex
}

type lokiPushRequest struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"`
}

func newLokiWriteSyncer(pushURL string, labels map[string]string) *lokiWriteSyncer {
	copiedLabels := make(map[string]string, len(labels))
	for key, value := range labels {
		copiedLabels[key] = value
	}

	return &lokiWriteSyncer{
		pushURL: strings.TrimSpace(pushURL),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		labels: copiedLabels,
	}
}

func (syncer *lokiWriteSyncer) Write(payload []byte) (int, error) {
	line := strings.TrimSpace(string(payload))
	if line == "" {
		return len(payload), nil
	}

	timestamp := fmt.Sprintf("%d", time.Now().UnixNano())
	labels := make(map[string]string, len(syncer.labels)+2)
	for key, value := range syncer.labels {
		labels[key] = value
	}
	formattedLine := line

	if parsed, ok := parseZapJSONLine(line); ok {
		if !parsed.ts.IsZero() {
			timestamp = fmt.Sprintf("%d", parsed.ts.UnixNano())
		}
		if parsed.level != "" {
			labels["level"] = parsed.level
		}
		if parsed.component != "" {
			labels["component"] = sanitizeLabelValue(parsed.component)
		}
		formattedLine = parsed.logfmt
	}

	requestBody := lokiPushRequest{
		Streams: []lokiStream{{
			Stream: labels,
			Values: [][2]string{{
				timestamp,
				formattedLine,
			}},
		}},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return 0, err
	}

	syncer.mu.Lock()
	defer syncer.mu.Unlock()

	req, err := http.NewRequest(http.MethodPost, syncer.pushURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := syncer.client.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("loki push failed: status=%d", resp.StatusCode)
	}

	return len(payload), nil
}

func (syncer *lokiWriteSyncer) Sync() error {
	return nil
}

type parsedZapLine struct {
	ts        time.Time
	level     string
	component string
	logfmt    string
}

func parseZapJSONLine(line string) (parsedZapLine, bool) {
	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return parsedZapLine{}, false
	}

	levelValue, _ := lookupFirst(entry, "level", "L", "lvl")
	parsed := parsedZapLine{
		level: normalizeLevel(asString(levelValue)),
	}

	tsValue, _ := lookupFirst(entry, "ts", "time", "T")
	if tsRaw := strings.TrimSpace(asString(tsValue)); tsRaw != "" {
		if ts, err := time.Parse(time.RFC3339Nano, tsRaw); err == nil {
			parsed.ts = ts
		} else if ts, err := time.Parse(time.RFC3339, tsRaw); err == nil {
			parsed.ts = ts
		}
	}

	if componentRaw, ok := lookupFirst(entry, "component", "comp", "logger", "N"); ok {
		parsed.component = strings.TrimSpace(asString(componentRaw))
	}

	keys := make([]string, 0, len(entry))
	for key := range entry {
		switch key {
		case "ts", "time", "T", "level", "L", "lvl", "msg", "message", "M", "component", "comp", "logger", "N":
			continue
		default:
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	messageValue, _ := lookupFirst(entry, "msg", "message", "M")
	message := strings.TrimSpace(asString(messageValue))
	if message == "" {
		message = "log"
	}

	builder := strings.Builder{}
	builder.WriteString("msg=")
	builder.WriteString(quoteLogfmt(message))

	for _, key := range keys {
		builder.WriteString(" ")
		builder.WriteString(sanitizeLogfmtKey(key))
		builder.WriteString("=")
		builder.WriteString(quoteLogfmt(asString(entry[key])))
	}

	parsed.logfmt = builder.String()
	return parsed, true
}

func asString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		bytes, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(bytes)
	}
}

func sanitizeLabelValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	replaced := strings.NewReplacer(" ", "_", "=", "_", "\"", "", "'", "").Replace(trimmed)
	return replaced
}

func sanitizeLogfmtKey(key string) string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return "field"
	}
	return strings.NewReplacer(" ", "_", "=", "_", "\"", "").Replace(trimmed)
}

func quoteLogfmt(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return "\"" + escaped + "\""
}

func lookupFirst(entry map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := entry[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func normalizeLevel(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return ""
	}
	trimmed = strings.Trim(trimmed, "\"")
	trimmed = strings.TrimSpace(trimmed)
	if strings.Contains(trimmed, "error") {
		return "error"
	}
	if strings.Contains(trimmed, "warn") {
		return "warn"
	}
	if strings.Contains(trimmed, "debug") {
		return "debug"
	}
	if strings.Contains(trimmed, "info") {
		return "info"
	}
	return trimmed
}
