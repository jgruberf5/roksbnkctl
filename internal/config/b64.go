package config

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// DecodeB64Field decodes a base64-encoded config field, naming the field in
// the error so the message points at what to fix.
//
// ALL whitespace is stripped first, not just the ends: GNU `base64` wraps its
// output at 76 columns, so the natural `export VAR=$(base64 file)` hands us a
// value with embedded newlines that strict StdEncoding rejects. The wrapping
// carries no information — a field fed through here is decode-then-use, never
// compared as a string — so tolerating it costs nothing and fails nobody.
func DecodeB64Field(field, value string) ([]byte, error) {
	compact := strings.Join(strings.Fields(value), "")
	b, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", field, err)
	}
	return b, nil
}
