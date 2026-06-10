package command

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"text/template"
	"unicode"

	"github.com/austiecodes/rolio/internal/client"
	"github.com/austiecodes/rolio/internal/store"
	"github.com/spf13/cobra"
)

type initTemplateData struct {
	Repo       string
	ServerAddr string
	DocsPath   string
	AuthMode   string
}

const initSettingsTomlTemplate = `version = 1
repo = "{{ .Repo }}"

[server]
addr = "{{ .ServerAddr }}"

[auth]
mode = "{{ .AuthMode }}"
token_env = "ROLIO_TOKEN"

[docs]
path = "{{ .DocsPath }}"

[cache]
metadata_ttl = "5m"
content_ttl = "24h"
materialize = "explicit"
`

const initMountsTomlTemplate = `version = 1

[[mounts]]
local = "{{ .DocsPath }}"
remote = "repo://self/{{ .DocsPath }}"
mode = "writable"
source = "default"
`

const ROLIOInstructionsStart = "<!-- ROLIO_START -->"
const ROLIOInstructionsEnd = "<!-- ROLIO_END -->"

//go:embed instructions/agents.md
var rolioInstructionsTemplate string

//go:embed instructions/skill.md
var rolioSkillTemplate string

//go:embed instructions/skill/references/*.md
var rolioSkillReferenceTemplates embed.FS

//go:embed instructions/pre_tool_use.sh
var preToolUseHookScript string

type initModes struct {
	md    bool
	skill bool
}

func NewInitCommand() *cobra.Command {
	var agent string
	var noInstructions bool
	var instructionMode string
	var repoName string
	var serverAddr string
	var docsPath string
	var authMode string
	var hookTarget string
	var hookScope string
	var registerRepo bool

	instructionMode = "md,skill"
	repoName = "github.com/user/repo"
	serverAddr = "http://127.0.0.1:7635"
	docsPath = "docs"
	authMode = "bearer"
	hookScope = "user"

	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize .rolio config in a repo",
		Long:  "Initialize .rolio/settings.toml and .rolio/mounts.toml in the target directory, add agent instructions, install the ROLIO skill to ~/.claude/skills/rolio/ and ~/.codex/skills/rolio/, and set up hooks in both user config dirs by default.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}

			if authMode != "bearer" && authMode != "none" {
				return fmt.Errorf("unsupported auth mode %q: use bearer or none", authMode)
			}
			var err error
			repoName, err = validateInitRepoName(repoName)
			if err != nil {
				return err
			}
			hookTarget = strings.ToLower(hookTarget)
			hookScope = strings.ToLower(hookScope)
			if hookTarget != "" {
				if hookScope != "user" && hookScope != "project" {
					return fmt.Errorf("unsupported --scope value %q: supported scopes are user and project", hookScope)
				}
				if hookTarget != "claude" && hookTarget != "codex" {
					return fmt.Errorf("unsupported --hook value %q: supported hooks are claude and codex", hookTarget)
				}
			}
			docsPath = cleanLocalDocsPath(docsPath)
			if docsPath == "" {
				docsPath = "docs"
			}

			modes, err := parseInitModes(instructionMode)
			if err != nil {
				return err
			}
			var target string
			if !noInstructions && modes.md {
				agent = strings.ToLower(agent)
				target, err = instructionTargetPath(dir, agent)
				if err != nil {
					return err
				}
			}

			rolioDir := filepath.Join(dir, ".rolio")
			settingsPath := filepath.Join(rolioDir, "settings.toml")
			mountsPath := filepath.Join(rolioDir, "mounts.toml")
			templateData := initTemplateData{
				Repo:       repoName,
				ServerAddr: serverAddr,
				DocsPath:   docsPath,
				AuthMode:   authMode,
			}

			if _, err := os.Stat(settingsPath); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "%s already exists, skipping\n", settingsPath)
			} else {
				if err := os.MkdirAll(rolioDir, 0o755); err != nil {
					return fmt.Errorf("create %s: %w", rolioDir, err)
				}
				settingsContent, err := renderInitTemplate("settings", initSettingsTomlTemplate, templateData)
				if err != nil {
					return err
				}
				if err := os.WriteFile(settingsPath, []byte(settingsContent), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", settingsPath, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", settingsPath)
			}

			if _, err := os.Stat(mountsPath); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "%s already exists, skipping\n", mountsPath)
			} else {
				if err := os.MkdirAll(rolioDir, 0o755); err != nil {
					return fmt.Errorf("create %s: %w", rolioDir, err)
				}
				mountsContent, err := renderInitTemplate("mounts", initMountsTomlTemplate, templateData)
				if err != nil {
					return err
				}
				if err := os.WriteFile(mountsPath, []byte(mountsContent), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", mountsPath, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", mountsPath)
			}

			if !noInstructions && modes.md {
				actual, err := upsertInstructions(target, docsPath)
				if err != nil {
					return err
				}
				if actual != target {
					fmt.Fprintf(cmd.OutOrStdout(), "updated ROLIO instructions in %s (resolved to %s)\n", target, actual)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "updated ROLIO instructions in %s\n", target)
				}
			}
			if !noInstructions && modes.skill {
				skillPaths, err := writeROLIOSkillToUserDir(docsPath)
				if err != nil {
					return err
				}
				for _, p := range skillPaths {
					fmt.Fprintf(cmd.OutOrStdout(), "updated ROLIO skill in %s\n", p)
				}
			}

			if !noInstructions {
				installClaude := hookTarget == "" || hookTarget == "claude"
				installCodex := hookTarget == "" || hookTarget == "codex"

				if installClaude && hookScope == "project" {
					if err := UpsertClaudeProjectHooks(dir); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "updated .claude/settings.json\n")
				}
				if installClaude && hookScope == "user" {
					if err := UpsertClaudeUserHooks(); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "updated ~/.claude/settings.json\n")
				}
				if installCodex && hookScope == "project" {
					if err := UpsertCodexProjectHooks(dir); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "updated .codex/hooks.json\n")
					fmt.Fprintf(cmd.OutOrStdout(), "Codex requires reviewing new project hooks. Run Codex and use /hooks to trust them.\n")
				}
				if installCodex && hookScope == "user" {
					if err := UpsertCodexUserHooks(); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "updated ~/.codex/hooks.json\n")
					fmt.Fprintf(cmd.OutOrStdout(), "Codex requires reviewing new user hooks. Run Codex and use /hooks to trust them.\n")
				}
			}

			if registerRepo {
				if err := client.New(serverAddr).RegisterRepo(cmd.Context(), repoName); err != nil {
					if !errors.Is(err, store.ErrRepoExists) {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "repo already registered %s\n", repoName)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "registered repo %s\n", repoName)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "initialized %s\n", rolioDir)
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "agent instructions target: agents or claude")
	cmd.Flags().BoolVar(&noInstructions, "no-instructions", false, "only create .rolio config, without writing agent instructions")
	cmd.Flags().StringVar(&instructionMode, "mode", instructionMode, "instruction outputs: md, skill, or md,skill")
	cmd.Flags().StringVar(&repoName, "repo", repoName, "logical repository name")
	cmd.Flags().StringVar(&serverAddr, "server", serverAddr, "rolio-server base URL")
	cmd.Flags().BoolVar(&registerRepo, "register", false, "register the repository with rolio-server")
	cmd.Flags().StringVar(&docsPath, "docs", docsPath, "local docs root")
	cmd.Flags().StringVar(&authMode, "auth", authMode, "auth mode: bearer or none")
	cmd.Flags().StringVar(&hookTarget, "hook", "", "restrict hooks to one agent: claude or codex (default: both)")
	cmd.Flags().StringVar(&hookScope, "scope", hookScope, "hook scope: user or project")
	return cmd
}

func renderInitTemplate(name, raw string, data initTemplateData) (string, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render %s template: %w", name, err)
	}
	return out.String(), nil
}

func validateInitRepoName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("invalid repo name: must not be empty")
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return "", fmt.Errorf("invalid repo name %q: must not start or end with /", name)
	}
	for _, r := range name {
		if unicode.IsSpace(r) {
			return "", fmt.Errorf("invalid repo name %q: must not contain whitespace", name)
		}
	}
	return name, nil
}

func parseInitModes(raw string) (initModes, error) {
	var modes initModes
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "md"
	}
	for _, part := range strings.Split(raw, ",") {
		mode := strings.ToLower(strings.TrimSpace(part))
		switch mode {
		case "md":
			modes.md = true
		case "skill":
			modes.skill = true
		default:
			return initModes{}, fmt.Errorf("unsupported init mode %q: supported modes are md, skill, and md,skill", mode)
		}
	}
	return modes, nil
}

func instructionTargetPath(dir, agent string) (string, error) {
	switch strings.ToLower(agent) {
	case "", "agent", "agents":
		return filepath.Join(dir, "AGENTS.md"), nil
	case "claude":
		return filepath.Join(dir, "CLAUDE.md"), nil
	default:
		return "", fmt.Errorf("unsupported agent %q: supported agents are agents and claude", agent)
	}
}

func upsertInstructions(target, docsPath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(target), err)
	}

	actual := target
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		actual = resolved
	}

	data, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", target, err)
	}

	block, err := renderROLIOInstructions(docsPath)
	if err != nil {
		return "", err
	}
	content := replaceMarkedBlock(string(data), block)
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", target, err)
	}
	return actual, nil
}

func writeSkillToDir(skillDir, docsPath string) (string, error) {
	target := filepath.Join(skillDir, "SKILL.md")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", skillDir, err)
	}
	content, err := renderROLIOSkill(docsPath)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", target, err)
	}
	if err := writeROLIOSkillReferences(skillDir, docsPath); err != nil {
		return "", err
	}
	return target, nil
}

func writeROLIOSkillToUserDir(docsPath string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home: %w", err)
	}
	var paths []string
	for _, dir := range []string{
		filepath.Join(home, ".claude", "skills", "rolio-skill"),
		filepath.Join(home, ".codex", "skills", "rolio-skill"),
	} {
		p, err := writeSkillToDir(dir, docsPath)
		if err != nil {
			return paths, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

func writeROLIOSkillReferences(skillDir, docsPath string) error {
	const sourceDir = "instructions/skill/references"
	targetDir := filepath.Join(skillDir, "references")

	entries, err := fs.ReadDir(rolioSkillReferenceTemplates, sourceDir)
	if err != nil {
		return fmt.Errorf("read ROLIO skill references: %w", err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", targetDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		sourcePath := path.Join(sourceDir, entry.Name())
		raw, err := rolioSkillReferenceTemplates.ReadFile(sourcePath)
		if err != nil {
			return fmt.Errorf("read ROLIO skill reference %s: %w", sourcePath, err)
		}
		content, err := renderROLIOSkillReference(entry.Name(), string(raw), docsPath)
		if err != nil {
			return err
		}
		target := filepath.Join(targetDir, entry.Name())
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
	}
	return nil
}

func replaceMarkedBlock(content, block string) string {
	start := strings.Index(content, ROLIOInstructionsStart)
	end := strings.Index(content, ROLIOInstructionsEnd)
	if start >= 0 && end >= start {
		end += len(ROLIOInstructionsEnd)
		next := content[end:]
		next = strings.TrimPrefix(next, "\n")
		content = content[:start] + strings.TrimSpace(block) + "\n" + next
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content
	}

	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if content != "" {
		content += "\n"
	}
	content += strings.TrimSpace(block) + "\n"
	return content
}

func renderROLIOInstructions(docsPath string) (string, error) {
	tmpl, err := template.New("rolio-instructions").Option("missingkey=error").Parse(rolioInstructionsTemplate)
	if err != nil {
		return "", fmt.Errorf("parse ROLIO instructions template: %w", err)
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, struct {
		DocsPath string
	}{
		DocsPath: docsPath,
	}); err != nil {
		return "", fmt.Errorf("render ROLIO instructions template: %w", err)
	}
	return out.String(), nil
}

func renderROLIOSkill(docsPath string) (string, error) {
	tmpl, err := template.New("rolio-skill").Option("missingkey=error").Parse(rolioSkillTemplate)
	if err != nil {
		return "", fmt.Errorf("parse ROLIO skill template: %w", err)
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, struct {
		DocsPath string
	}{
		DocsPath: docsPath,
	}); err != nil {
		return "", fmt.Errorf("render ROLIO skill template: %w", err)
	}
	return out.String(), nil
}

func renderROLIOSkillReference(name, raw, docsPath string) (string, error) {
	tmpl, err := template.New("rolio-skill-reference-" + name).Option("missingkey=error").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse ROLIO skill reference %s: %w", name, err)
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, struct {
		DocsPath string
	}{
		DocsPath: docsPath,
	}); err != nil {
		return "", fmt.Errorf("render ROLIO skill reference %s: %w", name, err)
	}
	return out.String(), nil
}

// UpsertClaudeProjectHooks writes SessionStart and PreToolUse hooks into the
// project-level .claude/settings.json. It merges with existing settings without
// overwriting other hooks or settings.
func UpsertClaudeProjectHooks(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve project dir: %w", err)
	}
	return upsertClaudeHooks(filepath.Join(absDir, ".claude"))
}

// UpsertClaudeUserHooks writes SessionStart and PreToolUse hooks into the
// user-level ~/.claude/settings.json.
func UpsertClaudeUserHooks() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home: %w", err)
	}
	return upsertClaudeHooks(filepath.Join(home, ".claude"))
}

func upsertClaudeHooks(claudeDir string) error {
	settingsPath := filepath.Join(claudeDir, "settings.json")

	rolioPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve rolio path: %w", err)
	}
	rolioPath, err = filepath.Abs(rolioPath)
	if err != nil {
		return fmt.Errorf("absolute rolio path: %w", err)
	}

	// Install the PreToolUse hook script into .claude/hooks/pre_tool_use.sh.
	if err := installPreToolUseHook(claudeDir); err != nil {
		return fmt.Errorf("install pre-tool-use hook: %w", err)
	}

	hookCommand := rolioPath + " hook session-start"

	var settings map[string]any
	data, err := os.ReadFile(settingsPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", settingsPath, err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", settingsPath, err)
		}
	}
	if settings == nil {
		settings = make(map[string]any)
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = make(map[string]any)
		settings["hooks"] = hooks
	}

	// Upsert SessionStart hook.
	sessionStart, _ := hooks["SessionStart"].([]any)
	if sessionStart == nil {
		sessionStart = []any{}
	}
	const sessionStartMatcher = "startup|resume"
	sessionStart, changed := upsertHookGroup(sessionStart, sessionStartMatcher, map[string]any{
		"type":    "command",
		"command": hookCommand,
	})
	if changed {
		hooks["SessionStart"] = sessionStart
	}

	// Upsert PreToolUse hook.
	preToolUse, _ := hooks["PreToolUse"].([]any)
	if preToolUse == nil {
		preToolUse = []any{}
	}
	hooksDir := filepath.Join(claudeDir, "hooks")
	preToolUseCmd := filepath.Join(hooksDir, "pre_tool_use.sh")
	preToolUse, changed2 := upsertHookGroup(preToolUse, "Bash", map[string]any{
		"type":    "command",
		"command": preToolUseCmd,
	})
	if changed2 {
		hooks["PreToolUse"] = preToolUse
	}

	if changed || changed2 {
		return writeClaudeSettings(settingsPath, claudeDir, settings)
	}
	return nil
}

// UpsertCodexProjectHooks writes SessionStart and PreToolUse hooks into the
// project-level .codex/hooks.json. It merges with existing hooks without
// overwriting unrelated events or duplicate hook entries.
func UpsertCodexProjectHooks(dir string) error {
	codexDir, useGitRootCommand, err := codexProjectHooksDir(dir)
	if err != nil {
		return err
	}
	return upsertCodexHooks(codexDir, useGitRootCommand)
}

// UpsertCodexUserHooks writes SessionStart and PreToolUse hooks into the
// user-level ~/.codex/hooks.json.
func UpsertCodexUserHooks() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home: %w", err)
	}
	return upsertCodexHooks(filepath.Join(home, ".codex"), false)
}

func codexProjectHooksDir(dir string) (string, bool, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", false, fmt.Errorf("absolute project dir: %w", err)
	}
	out, err := exec.Command("git", "-C", absDir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return filepath.Join(absDir, ".codex"), false, nil
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return filepath.Join(absDir, ".codex"), false, nil
	}
	return filepath.Join(root, ".codex"), true, nil
}

func upsertCodexHooks(codexDir string, useGitRootCommand bool) error {
	hooksPath := filepath.Join(codexDir, "hooks.json")

	rolioPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve rolio path: %w", err)
	}
	rolioPath, err = filepath.Abs(rolioPath)
	if err != nil {
		return fmt.Errorf("absolute rolio path: %w", err)
	}

	if err := installPreToolUseHook(codexDir); err != nil {
		return fmt.Errorf("install pre-tool-use hook: %w", err)
	}

	var config map[string]any
	data, err := os.ReadFile(hooksPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", hooksPath, err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse %s: %w", hooksPath, err)
		}
	}
	if config == nil {
		config = make(map[string]any)
	}

	hooks, _ := config["hooks"].(map[string]any)
	if hooks == nil {
		hooks = make(map[string]any)
		config["hooks"] = hooks
	}

	sessionStart, _ := hooks["SessionStart"].([]any)
	if sessionStart == nil {
		sessionStart = []any{}
	}
	sessionStart, changed := upsertHookGroup(sessionStart, "startup|resume", map[string]any{
		"type":          "command",
		"command":       shellCommandArg(rolioPath) + " hook session-start",
		"statusMessage": "Refreshing ROLIO docs",
	})
	if changed {
		hooks["SessionStart"] = sessionStart
	}

	preToolUse, _ := hooks["PreToolUse"].([]any)
	if preToolUse == nil {
		preToolUse = []any{}
	}
	preToolUseCmd := shellCommandArg(filepath.Join(codexDir, "hooks", "pre_tool_use.sh"))
	if useGitRootCommand {
		preToolUseCmd = `"$(git rev-parse --show-toplevel)/.codex/hooks/pre_tool_use.sh"`
	}
	preToolUse, changed2 := upsertHookGroup(preToolUse, "Bash", map[string]any{
		"type":          "command",
		"command":       "/bin/bash " + preToolUseCmd,
		"statusMessage": "Preparing ROLIO audit context",
	})
	if changed2 {
		hooks["PreToolUse"] = preToolUse
	}

	if changed || changed2 {
		return writeJSONFile(hooksPath, codexDir, config)
	}
	return nil
}

func shellCommandArg(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// upsertHookGroup ensures a matcher group contains the target hook entry.
func upsertHookGroup(groups []any, matcher string, target map[string]any) ([]any, bool) {
	for _, group := range groups {
		g, ok := group.(map[string]any)
		if !ok || g["matcher"] != matcher {
			continue
		}
		hookList, _ := g["hooks"].([]any)
		targetCmd, _ := target["command"].(string)
		for _, h := range hookList {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if cmd, _ := hm["command"].(string); cmd == targetCmd {
				return groups, false
			}
		}
		g["hooks"] = append(hookList, target)
		return groups, true
	}
	return append(groups, map[string]any{
		"matcher": matcher,
		"hooks":   []any{target},
	}), true
}

// installPreToolUseHook writes the pre_tool_use.sh script into a tool-specific
// hooks directory such as .claude/hooks or .codex/hooks.
func installPreToolUseHook(configDir string) error {
	hooksDir := filepath.Join(configDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", hooksDir, err)
	}
	scriptPath := filepath.Join(hooksDir, "pre_tool_use.sh")
	return os.WriteFile(scriptPath, []byte(preToolUseHookScript), 0o755)
}

func writeClaudeSettings(settingsPath, claudeDir string, settings map[string]any) error {
	return writeJSONFile(settingsPath, claudeDir, settings)
}

func writeJSONFile(path, dir string, value any) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
