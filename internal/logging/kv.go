package logging

import (
	"strconv"
	"strings"

	"go.uber.org/zap"
)

func InfoKV(component, message string) {
	msg, fields := parseKV(component, message)
	zap.S().Infow(msg, fields...)
}

func DebugKV(component, message string) {
	msg, fields := parseKV(component, message)
	zap.S().Debugw(msg, fields...)
}

func ErrorKV(component, message string) {
	msg, fields := parseKV(component, message)
	zap.S().Errorw(msg, fields...)
}

func parseKV(component, raw string) (string, []any) {
	message := strings.TrimSpace(raw)
	if message == "" {
		message = "log"
	}

	fields := make([]any, 0, 16)
	if strings.TrimSpace(component) != "" {
		fields = append(fields, "component", strings.TrimSpace(component))
	}

	tokens := strings.Fields(message)
	firstKeyIdx := -1
	for index, token := range tokens {
		if isKVToken(token) {
			firstKeyIdx = index
			break
		}
	}

	if firstKeyIdx == -1 {
		return message, fields
	}

	head := strings.TrimSpace(strings.Join(tokens[:firstKeyIdx], " "))
	if head != "" {
		message = head
	}

	for _, token := range tokens[firstKeyIdx:] {
		if !isKVToken(token) {
			continue
		}
		parts := strings.SplitN(token, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		if key == "" {
			continue
		}
		fields = append(fields, key, parseValue(value))
	}

	return message, fields
}

func isKVToken(token string) bool {
	if !strings.Contains(token, "=") {
		return false
	}
	parts := strings.SplitN(token, "=", 2)
	return strings.TrimSpace(parts[0]) != ""
}

func parseValue(value string) any {
	if parsedBool, err := strconv.ParseBool(value); err == nil {
		return parsedBool
	}
	if parsedInt, err := strconv.ParseInt(value, 10, 64); err == nil {
		return parsedInt
	}
	if parsedFloat, err := strconv.ParseFloat(value, 64); err == nil {
		return parsedFloat
	}
	return value
}
