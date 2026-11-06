package inbox

import (
	"bytes"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Limits is everything a message may carry. The one place they are set:
// DefaultLimits, shown to the client by GET /v1/inbox.
type Limits struct {
	AttachmentsPerMessage   int
	BytesPerAttachment      int64
	BytesPerMessage         int64
	MessagesPerDay          int // accepted messages per tenant and Argentine day
	MessagesPerSenderPerDay int // accepted messages per sender and day
	RefusedPerDay           int // refused messages that still leave a receipt; past it they are dropped
}

// DefaultLimits are the limits agreed with lead-4 (D-22). Not measured:
// a guess at what a client's statements need, with room.
var DefaultLimits = Limits{
	AttachmentsPerMessage:   10,
	BytesPerAttachment:      20 << 20, // the ledger's own limit per file: a larger one never reaches it
	BytesPerMessage:         25 << 20,
	MessagesPerDay:          200,
	MessagesPerSenderPerDay: 50,
	RefusedPerDay:           50,
}

// Kinds a statement comes in. Anything else is refused per attachment.
var Kinds = []string{"csv", "txt", "xls", "xlsx", "pdf"}

// kindOf says which statement kind an attachment is, by its extension and
// its first bytes both: an extension alone is a claim, the content decides.
// It returns "" for anything that is not a statement kind.
func kindOf(filename string, head []byte) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	switch ext {
	case "pdf":
		if bytes.HasPrefix(head, []byte("%PDF-")) {
			return ext
		}
	case "xlsx":
		if bytes.HasPrefix(head, []byte("PK\x03\x04")) {
			return ext
		}
	case "xls":
		if bytes.HasPrefix(head, []byte("\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1")) {
			return ext
		}
	case "csv", "txt":
		if looksLikeText(head) {
			return ext
		}
	}
	return ""
}

// looksLikeText: no NUL byte and no control characters other than tabs and
// line breaks. Latin-1 exports are text too, so invalid UTF-8 alone does
// not disqualify.
func looksLikeText(head []byte) bool {
	if len(head) == 0 {
		return false
	}
	for len(head) > 0 {
		r, size := utf8.DecodeRune(head)
		head = head[size:]
		if r == utf8.RuneError && size == 1 {
			continue // a Latin-1 byte
		}
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}
