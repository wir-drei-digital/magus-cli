package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/skill"
)

const skillFileName = "magus.md"

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install the magus agent skill for Claude Code, Codex, and similar agents",
	}
	cmd.AddCommand(skillInstallCmd(), skillUninstallCmd(), skillShowCmd())
	return cmd
}

func skillInstallCmd() *cobra.Command {
	var target, path string
	var update bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the magus skill file (Claude Code users: prefer the plugin install)",
		Long: `Writes the embedded magus agent skill to your agent's skills directory.

For Claude Code, the preferred path is the marketplace plugin install:

    /plugin marketplace add wir-drei-digital/magus-cli
    /plugin install magus@wir-drei-digital

Claude Code only discovers skills through its plugin system; the legacy
~/.claude/skills/ path is NOT auto-loaded. This command still writes there for
backward compatibility (target "claude-code" or "claude-code-legacy"). For
Codex and other agents that read ~/.<agent>/skills/, use --target codex (or
--path) as before.

Without --update, install refuses to overwrite an existing file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if path == "" && (target == "claude-code" || target == "claude-code-legacy") {
				printClaudeCodePluginBanner(cmd)
			}
			dest, err := resolveSkillPath(target, path)
			if err != nil {
				return err
			}
			if err := writeSkill(dest, update); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed skill to %s\n", dest)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "claude-code", "claude-code | claude-code-legacy | codex")
	cmd.Flags().StringVar(&path, "path", "", "explicit destination path (overrides --target)")
	cmd.Flags().BoolVar(&update, "update", false, "overwrite an existing file")
	return cmd
}

func printClaudeCodePluginBanner(cmd *cobra.Command) {
	w := cmd.OutOrStdout()
	fmt.Fprintln(w, "Claude Code discovers skills via its plugin system, not via ~/.claude/skills/.")
	fmt.Fprintln(w, "For the best experience, run inside Claude Code:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "    /plugin marketplace add wir-drei-digital/magus-cli")
	fmt.Fprintln(w, "    /plugin install magus@wir-drei-digital")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Continuing with the legacy ~/.claude/skills/ install for backward compatibility...")
	fmt.Fprintln(w)
}

func skillUninstallCmd() *cobra.Command {
	var target, path string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the installed magus skill file",
		RunE: func(cmd *cobra.Command, args []string) error {
			dest, err := resolveSkillPath(target, path)
			if err != nil {
				return err
			}
			if err := os.Remove(dest); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("skill is not installed at %s", dest)
				}
				return err
			}
			fmt.Printf("Removed %s\n", dest)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "claude-code", "claude-code | claude-code-legacy | codex")
	cmd.Flags().StringVar(&path, "path", "", "explicit destination path (overrides --target)")
	return cmd
}

func skillShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the embedded skill content to stdout",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Print(skill.Content)
			return nil
		},
	}
}

func resolveSkillPath(target, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch target {
	case "claude-code", "claude-code-legacy":
		return filepath.Join(home, ".claude", "skills", skillFileName), nil
	case "codex":
		return filepath.Join(home, ".codex", "skills", skillFileName), nil
	default:
		return "", fmt.Errorf("unknown --target %q (supported: claude-code, claude-code-legacy, codex)", target)
	}
}

func writeSkill(dest string, update bool) error {
	if _, err := os.Stat(dest); err == nil && !update {
		return fmt.Errorf("%s already exists (pass --update to overwrite)", dest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, []byte(skill.Content), 0o644)
}
