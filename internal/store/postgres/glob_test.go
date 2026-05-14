package postgres

import (
	"regexp"
	"testing"
)

func TestGlobToRegex(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// **/*.md — matches any depth .md file
		{"**/*.md", "docs/readme.md", true},
		{"**/*.md", "docs/api/chat.md", true},
		{"**/*.md", "readme.md", true},
		{"**/*.md", "src/main.go", false},
		{"**/*.md", "docs/readme.txt", false},

		// docs/**/*.go — matches under docs/ at any depth
		{"docs/**/*.go", "docs/main.go", true},
		{"docs/**/*.go", "docs/api/handler.go", true},
		{"docs/**/*.go", "src/main.go", false},
		{"docs/**/*.go", "docs/", false},

		// src/*.go — single level only
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/pkg/util.go", false},

		// *openai* — match in filename only
		{"*openai*", "openai-go.md", true},
		{"*openai*", "docs/openai-go.md", false},
		{"*openai*", "my-openai-guide.md", true},

		// **/test/** — directory named test at any depth
		{"**/test/**", "test/main.go", true},
		{"**/test/**", "a/test/b/c.md", true},
		{"**/test/**", "src/testing/util.go", false},

		// ** — match everything
		{"**", "anything/goes.md", true},
		{"**", "deeply/nested/path/file.txt", true},

		// ? — single char
		{"?.md", "a.md", true},
		{"?.md", "ab.md", false},
		{"?.md", "/a.md", false},

		// exact path
		{"docs/readme.md", "docs/readme.md", true},
		{"docs/readme.md", "docs/other.md", false},

		// dot in extension
		{"**/*.go", "src/main.go", true},
		{"**/*.go", "src/pkg.test.go", true},

		// leading ./ or /
		{"./docs/*.md", "docs/readme.md", true},
		{"/docs/*.md", "docs/readme.md", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.path, func(t *testing.T) {
			regex, err := globToRegex(tt.pattern)
			if err != nil {
				t.Fatalf("globToRegex(%q) error = %v", tt.pattern, err)
			}
			re, err := regexp.Compile("^" + regex + "$")
			if err != nil {
				t.Fatalf("compile regex %q error = %v", regex, err)
			}
			got := re.MatchString(tt.path)
			if got != tt.want {
				t.Errorf("globToRegex(%q) = %q, match(%q) = %v, want %v", tt.pattern, regex, tt.path, got, tt.want)
			}
		})
	}
}

func TestGlobToRegexEmpty(t *testing.T) {
	_, err := globToRegex("")
	if err == nil {
		t.Fatal("globToRegex('') error = nil, want error")
	}
}
