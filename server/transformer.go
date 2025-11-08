package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func TransformCypherToSQL(ctx context.Context, binPath, cypher string) (string, error) {
	cmd := exec.CommandContext(ctx, binPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(cypher)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("transformer exec: %w; stderr=%s", err, truncate(stderr.String(), 4000))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
