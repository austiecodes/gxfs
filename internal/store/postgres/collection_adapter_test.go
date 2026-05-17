package postgres

import (
	"strings"
	"testing"
)

func TestCollectionNameRegex(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"my-collection", true},
		{"my_collection", true},
		{"mycollection123", true},
		{"abc", true},
		{"a-b_c123", true},
		{"MyCollection", false},  // uppercase
		{"my collection", false}, // space
		{"my.collection", false}, // dot
		{"my/collection", false}, // slash
		{"", false},              // empty
		{"my collection", false}, // space
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectionNameRegex.MatchString(tt.name)
			if got != tt.valid {
				t.Errorf("collectionNameRegex.MatchString(%q) = %v, want %v", tt.name, got, tt.valid)
			}
		})
	}
}

func TestParseRepoRef(t *testing.T) {
	tests := []struct {
		name        string
		ref         string
		wantRepo    string
		wantPath    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "simple repo ref",
			ref:      "repo://my-repo/docs/readme.md",
			wantRepo: "my-repo",
			wantPath: "/docs/readme.md",
		},
		{
			name:     "repo with slash in name (URL-encoded)",
			ref:      "repo://github%2Fopenai-go/docs/readme.md",
			wantRepo: "github/openai-go",
			wantPath: "/docs/readme.md",
		},
		{
			name:        "non-repo ref rejected",
			ref:         "collection://my-col/docs/readme.md",
			wantErr:     true,
			errContains: "must start with repo://",
		},
		{
			name:        "missing path",
			ref:         "repo://my-repo",
			wantErr:     true,
			errContains: "invalid repo ref format",
		},
		{
			name:     "root path allowed",
			ref:      "repo://my-repo/",
			wantRepo: "my-repo",
			wantPath: "/",
		},
		{
			name:        "invalid URL encoding (no slash)",
			ref:         "repo://github%2Fopenai-go%",
			wantErr:     true,
			errContains: "invalid repo ref format",
		},
		{
			name:        "invalid URL encoding in repo name",
			ref:         "repo://github%2%/docs/readme.md",
			wantErr:     true,
			errContains: "invalid repo name encoding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, path, err := parseRepoRef(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseRepoRef(%q) expected error, got nil", tt.ref)
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("parseRepoRef(%q) error = %v, want error containing %q", tt.ref, err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("parseRepoRef(%q) unexpected error: %v", tt.ref, err)
				return
			}
			if repo != tt.wantRepo {
				t.Errorf("parseRepoRef(%q) repo = %q, want %q", tt.ref, repo, tt.wantRepo)
			}
			if path != tt.wantPath {
				t.Errorf("parseRepoRef(%q) path = %q, want %q", tt.ref, path, tt.wantPath)
			}
		})
	}
}
