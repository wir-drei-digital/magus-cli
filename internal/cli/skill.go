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
		Short: "Install the magus skill file",
		Long: `Writes the embedded magus agent skill to your agent's skills directory.

Default target is Claude Code (~/.claude/skills/magus.md). Pass --target codex
to install for Codex (~/.codex/skills/magus.md), or --path to choose any
location.

Without --update, install refuses to overwrite an existing file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dest, err := resolveSkillPath(target, path)
			if err != nil {
				return err
			}
			if err := writeSkill(dest, update); err != nil {
				return err
			}
			fmt.Printf("Installed skill to %s\n", dest)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "claude-code", "claude-code | codex")
	cmd.Flags().StringVar(&path, "path", "", "explicit destination path (overrides --target)")
	cmd.Flags().BoolVar(&update, "update", false, "overwrite an existing file")
	return cmd
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
	cmd.Flags().StringVar(&target, "target", "claude-code", "claude-code | codex")
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
	case "claude-code":
		return filepath.Join(home, ".claude", "skills", skillFileName), nil
	case "codex":
		return filepath.Join(home, ".codex", "skills", skillFileName), nil
	default:
		return "", fmt.Errorf("unknown --target %q (supported: claude-code, codex)", target)
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
