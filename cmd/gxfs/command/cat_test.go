package command

import "testing"

func TestParseDocsetRef(t *testing.T) {
	name, path, err := parseDocsetRef("docset://best-practices/go/errors.md")
	if err != nil {
		t.Fatalf("parseDocsetRef() error = %v", err)
	}
	if name != "best-practices" || path != "/go/errors.md" {
		t.Fatalf("parseDocsetRef() = (%q, %q), want (%q, %q)", name, path, "best-practices", "/go/errors.md")
	}
}

func TestParseDocsetRefRejectsMissingPath(t *testing.T) {
	if _, _, err := parseDocsetRef("docset://best-practices"); err == nil {
		t.Fatal("parseDocsetRef() error = nil, want missing path rejection")
	}
}
