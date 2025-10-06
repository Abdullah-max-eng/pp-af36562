package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// TransformCypherToSQL runs the Rust transformer and returns the SQL it prints.
func TransformCypherToSQL(ctx context.Context, binPath, cypher string) (string, error) {
	// Example: transformer-rs "<cypher>"
	cmd := exec.CommandContext(ctx, binPath, cypher)

	// cmd.Output() will honor ctx: if ctx is done, the process is killed.
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("transformer exec: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
