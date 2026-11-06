package relaylog

import (
	"reflect"
	"strings"
	"unicode/utf8"
)

// escapeRelayLogDatabaseText keeps valid text unchanged and makes bytes that
// PostgreSQL text cannot store visible as \xNN. In particular, jsoniter errors
// can contain an actual 0xff; JSON serialization masks it, but attempt.msg is a
// separate text column. This is a database presentation copy, never a rewrite
// of captured request/response bytes or downstream traffic.
func escapeRelayLogDatabaseText(value string) string {
	if utf8.ValidString(value) && !strings.ContainsRune(value, 0) {
		return value
	}
	const hex = "0123456789abcdef"
	var escaped strings.Builder
	escaped.Grow(len(value))
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		if r == 0 || (r == utf8.RuneError && size == 1) {
			escaped.WriteString(`\x`)
			escaped.WriteByte(hex[value[0]>>4])
			escaped.WriteByte(hex[value[0]&15])
		} else {
			escaped.WriteString(value[:size])
		}
		value = value[size:]
	}
	return escaped.String()
}

// Only scalar string columns of our persistence row copies are inspected.
// JSON-serialized fields already encode NUL safely; []byte blob payloads and
// nested references must remain untouched. Covering all scalar strings also
// protects metadata supplied by arbitrary upstream headers and filenames.
func escapeRelayLogRowText(row any) {
	value := reflect.ValueOf(row).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.String && field.CanSet() {
			field.SetString(escapeRelayLogDatabaseText(field.String()))
		}
	}
}
