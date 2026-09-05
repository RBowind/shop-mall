package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"shop-mall/backend/internal/admin"
)

// runBootstrap wires the controlled input path for the admin-bootstrap
// command. The function accepts the os.Args slice so tests can drive it
// without mutating the global state; the standard main path uses os.Args[1:]
// directly.
func runBootstrap(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if getenv == nil {
		getenv = os.Getenv
	}

	fs := flag.NewFlagSet("admin-bootstrap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		username = fs.String("username", "", "administrator username (required)")
		password = fs.String("password", "", "administrator password (required)")
		role     = fs.String("role", "super_admin", "role name to assign (default: super_admin)")
		format   = fs.String("format", "text", "output format: text or json")
		actor    = fs.String("actor", "cli-bootstrap", "actor identifier written into the audit log")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	input := admin.BootstrapInput{
		Username:  strings.TrimSpace(*username),
		Password:  *password,
		RoleName:  strings.TrimSpace(*role),
		ActorName: strings.TrimSpace(*actor),
	}
	if input.Username == "" || input.Password == "" || input.RoleName == "" {
		_, _ = fmt.Fprintln(stderr, "admin-bootstrap: --username, --password and --role are required")
		return flag.ErrHelp
	}

	if err := admin.ValidateUsername(input.Username); err != nil {
		_, _ = fmt.Fprintf(stderr, "admin-bootstrap: invalid username: %v\n", err)
		return err
	}
	if err := admin.ValidatePassword(input.Password); err != nil {
		_, _ = fmt.Fprintf(stderr, "admin-bootstrap: weak password: %v\n", err)
		return errBootstrapWeakPassword
	}

	databaseURL := strings.TrimSpace(getenv("DATABASE_URL"))
	if databaseURL == "" {
		_, _ = fmt.Fprintln(stderr, "admin-bootstrap: DATABASE_URL is required")
		return errBootstrapMissingDatabase
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	service, result, err := runBootstrapService(ctx, databaseURL, input)
	if err != nil {
		switch {
		case errors.Is(err, admin.ErrAdministratorExists):
			_, _ = fmt.Fprintln(stderr, "admin-bootstrap: administrator already exists")
			return errBootstrapExists
		case errors.Is(err, admin.ErrUnknownRole):
			_, _ = fmt.Fprintln(stderr, "admin-bootstrap: unknown role")
			return err
		case errors.Is(err, admin.ErrWeakPassword):
			_, _ = fmt.Fprintln(stderr, "admin-bootstrap: weak password")
			return errBootstrapWeakPassword
		case errors.Is(err, admin.ErrInvalidUsername):
			_, _ = fmt.Fprintf(stderr, "admin-bootstrap: invalid username: %v\n", err)
			return err
		default:
			_, _ = fmt.Fprintf(stderr, "admin-bootstrap: %v\n", err)
			return err
		}
	}
	_ = service
	return writeBootstrapOutput(stdout, input, result, *format)
}

// writeBootstrapOutput renders the bootstrap result in either the
// human-readable text format or the JSON format. The format flag is read
// directly because tests pass an explicit value.
func writeBootstrapOutput(stdout io.Writer, input admin.BootstrapInput, result admin.BootstrapResult, format string) error {
	switch strings.ToLower(format) {
	case "json":
		payload := map[string]any{
			"admin_id":      result.AdminID,
			"username":      result.Username,
			"role_name":     result.RoleName,
			"token_version": result.TokenVersion,
			"actor":         input.ActorName,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(encoded))
	case "text", "":
		_, _ = fmt.Fprintf(stdout, "admin id: %d\nusername: %s\nrole: %s\ntoken_version: %d\n",
			result.AdminID, result.Username, result.RoleName, result.TokenVersion)
	default:
		return fmt.Errorf("admin-bootstrap: unsupported format %q", format)
	}
	return nil
}
