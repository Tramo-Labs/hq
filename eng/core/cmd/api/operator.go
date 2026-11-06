package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// createOperator implements `api create-operator -email X -tenant <uuid>
// -password-stdin`: the only way to grant the operator role. The password
// is read from the first line of stdin, never from a flag or the
// environment, so it stays out of shell history and process listings.
// Idempotent. Uses DATABASE_URL (the runtime role, which switches to
// tramo_system for the write).
func createOperator(ctx context.Context, args []string, stdin io.Reader, out io.Writer, getenv func(string) string, argon identity.Argon2Params) error {
	fs := flag.NewFlagSet("create-operator", flag.ContinueOnError)
	fs.SetOutput(out)
	email := fs.String("email", "", "operator's email")
	tenant := fs.String("tenant", "", "tenant id (uuid)")
	fromStdin := fs.Bool("password-stdin", false, "read the password from the first line of stdin (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" || *tenant == "" || !*fromStdin {
		return errors.New("usage: api create-operator -email X -tenant <uuid> -password-stdin")
	}
	tid, err := uuid.Parse(*tenant)
	if err != nil {
		return errors.New("-tenant must be a uuid")
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("reading the password: %w", err)
	}
	password := strings.TrimRight(line, "\r\n")
	url := getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	d, err := db.Open(ctx, url)
	if err != nil {
		return err
	}
	defer d.Close()
	r, err := identity.CreateOperator(ctx, d, argon, *email, tid, password)
	if err != nil {
		return err
	}
	switch {
	case r.Added:
		fmt.Fprintf(out, "operator added: user %s, tenant %s (new user: %t)\n", r.UserID, tid, r.UserCreated)
	case r.PasswordSet:
		fmt.Fprintf(out, "already operator: user %s, tenant %s; password set (it had none)\n", r.UserID, tid)
	default:
		fmt.Fprintf(out, "already operator: user %s, tenant %s; nothing changed\n", r.UserID, tid)
	}
	return nil
}
