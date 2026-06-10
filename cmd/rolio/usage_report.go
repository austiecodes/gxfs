package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/austiecodes/rolio/internal/client"
	"github.com/austiecodes/rolio/internal/store"
)

const defaultUsageReportTimeout = 750 * time.Millisecond

type usageReportRequest struct {
	ServerAddr string
	Repo       string
	LogID      string
	SessionID  string
	Command    string
	Args       []string
	DurationMs int64
	ExitCode   int
}

func maybeReportUsageEvent(req usageReportRequest) {
	if req.ServerAddr == "" || req.Command == "" || os.Getenv("ROLIO_USAGE_REPORT") == "0" {
		return
	}
	if req.LogID == "" && os.Getenv("ROLIO_USAGE_REPORT") != "1" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), usageReportTimeout())
	defer cancel()

	cli := client.New(req.ServerAddr)
	cli.SetClientRepo(req.Repo)
	cli.SetLogID(req.LogID)
	_, _ = cli.RecordUsageEvent(ctx, store.UsageEvent{
		LogID:      req.LogID,
		SessionID:  req.SessionID,
		ClientRepo: req.Repo,
		Command:    req.Command,
		ExitCode:   req.ExitCode,
		DurationMs: req.DurationMs,
		EventKind:  store.UsageEventKindCLICommand,
		Payload:    buildUsagePayload(req.Args),
	})
}

func usageReportTimeout() time.Duration {
	raw := os.Getenv("ROLIO_USAGE_REPORT_TIMEOUT")
	if raw == "" {
		return defaultUsageReportTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultUsageReportTimeout
	}
	return d
}

func buildUsagePayload(args []string) json.RawMessage {
	payload := map[string]any{
		"args":      redactUsageArgs(args),
		"arg_count": len(args),
	}
	if len(args) > 1 && isGroupedUsageCommand(args[0]) {
		payload["subcommand"] = args[1]
	}
	if query, ok := usageQuery(args); ok {
		payload["query"] = query
	}
	if targetPath, ok := usagePath(args); ok {
		payload["path"] = targetPath
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func redactUsageArgs(args []string) []string {
	out := append([]string(nil), args...)
	if len(out) == 0 {
		return out
	}
	switch out[0] {
	case "write", "edit":
		seenPositionals := 0
		for i := 1; i < len(out); i++ {
			if strings.HasPrefix(out[i], "-") {
				continue
			}
			seenPositionals++
			if seenPositionals > 1 {
				out[i] = "[redacted]"
			}
		}
	}
	return out
}

func isGroupedUsageCommand(command string) bool {
	switch command {
	case "config", "docset", "hook", "mount", "repo", "sync":
		return true
	default:
		return false
	}
}

func usageQuery(args []string) (string, bool) {
	if len(args) < 2 {
		return "", false
	}
	switch args[0] {
	case "search", "locate":
		return firstUsagePositional(args[1:])
	case "grep":
		return firstUsagePositional(args[1:])
	default:
		return "", false
	}
}

func usagePath(args []string) (string, bool) {
	if len(args) < 2 {
		return "", false
	}
	switch args[0] {
	case "cat", "ls", "stat", "tree", "rm", "write", "edit":
		return firstUsagePositional(args[1:])
	case "grep":
		pos := usagePositionals(args[1:])
		if len(pos) >= 2 {
			return pos[1], true
		}
	case "find":
		return firstUsagePositional(args[1:])
	}
	return "", false
}

func firstUsagePositional(args []string) (string, bool) {
	pos := usagePositionals(args)
	if len(pos) == 0 {
		return "", false
	}
	return pos[0], true
}

func usagePositionals(args []string) []string {
	pos := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		pos = append(pos, arg)
	}
	return pos
}
