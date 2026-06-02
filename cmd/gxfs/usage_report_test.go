package main

import (
	"encoding/json"
	"testing"
)

func TestBuildUsagePayloadSearchIncludesQuery(t *testing.T) {
	payload := buildUsagePayload([]string{"search", "auth tokens", "--limit", "5"})
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["query"] != "auth tokens" {
		t.Fatalf("query = %v, want auth tokens", got["query"])
	}
	args, ok := got["args"].([]any)
	if !ok || len(args) != 4 || args[0] != "search" {
		t.Fatalf("args = %#v, want raw search args", got["args"])
	}
}

func TestBuildUsagePayloadRedactsWriteContent(t *testing.T) {
	payload := buildUsagePayload([]string{"write", "docs/a.md", "secret content"})
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["path"] != "docs/a.md" {
		t.Fatalf("path = %v, want docs/a.md", got["path"])
	}
	args := got["args"].([]any)
	if args[2] != "[redacted]" {
		t.Fatalf("args = %#v, want redacted content", args)
	}
}
