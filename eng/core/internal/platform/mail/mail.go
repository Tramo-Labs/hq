// Package mail sends the few emails the core writes (invitations). The only
// transport built is Outbox: it appends each message to a local file, mode
// 0600, for `make dev` and tests. There is no SMTP transport: nothing in
// this repository sends a real email.
package mail

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Message is one plain text email.
type Message struct {
	To, Subject, Body string
}

// Sender delivers a message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Outbox appends messages to a file, separated by a "From " line like an
// mbox. The file is created 0600 and forced to 0600 on every write, since
// it holds invitation links.
type Outbox struct {
	Path string
	mu   sync.Mutex
}

// Send appends m to the outbox file.
func (o *Outbox) Send(_ context.Context, m Message) error {
	if strings.ContainsAny(m.To+m.Subject, "\r\n") {
		return fmt.Errorf("mail: header with a line break")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	f, err := os.OpenFile(o.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("mail: outbox: %w", err)
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return fmt.Errorf("mail: outbox: %w", err)
	}
	_, err = fmt.Fprintf(f, "From tramo %s\nTo: %s\nSubject: %s\n\n%s\n\n",
		time.Now().UTC().Format(time.RFC3339), m.To, m.Subject, m.Body)
	return err
}
