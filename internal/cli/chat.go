package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/magus-cli/internal/chat"
	"github.com/wir-drei-digital/magus-cli/internal/config"
	"github.com/wir-drei-digital/magus-cli/internal/localtool"
)

type chatOptions struct {
	WSURL     string
	Token     string
	UserAgent string
	Root      string
	In        io.Reader
	Out       io.Writer
	ConfigDir string // for the audit log location
}

func newChatCmd() *cobra.Command {
	var rootDir string

	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Chat with the cloud agent, with local file access",
		Long: `Open an interactive chat with the Magus cloud agent. The agent can request
local tools (e.g. read_file); each request is shown for your approval before it runs.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			token := config.ResolveToken(cfg, profile)
			if token == "" {
				return fmt.Errorf("no token configured (run `magus login`)")
			}
			wsURL, err := chat.WSURL(config.ResolveAPIURL(cfg, profile, DefaultAPIURL))
			if err != nil {
				return err
			}
			if rootDir == "" {
				if rootDir, err = os.Getwd(); err != nil {
					return err
				}
			}
			confDir, _ := config.DefaultDir()

			return runChat(cmd.Context(), chatOptions{
				WSURL:     wsURL,
				Token:     token,
				UserAgent: "magus-cli/" + Version,
				Root:      rootDir,
				In:        os.Stdin,
				Out:       os.Stdout,
				ConfigDir: confDir,
			})
		},
	}
	cmd.Flags().StringVar(&rootDir, "root", "", "root directory local file tools are confined to (default: cwd)")
	return cmd
}

func runChat(ctx context.Context, opts chatOptions) error {
	cli, err := chat.Dial(ctx, opts.WSURL, opts.Token, opts.UserAgent)
	if err != nil {
		return err
	}
	defer cli.Close()

	reg := localtool.Registry{"read_file": &localtool.ReadFile{Root: opts.Root, MaxBytes: 256 * 1024}}

	// Load persisted permissions for the policy. A config that will not load
	// leaves every tier at its zero value — "prompt", the fail-safe default,
	// never something more permissive — but say so out loud, because a user who
	// set read = "deny" deserves to know their kill switch is not in force.
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		fmt.Fprintf(opts.Out, "warning: could not load config (%v); every tool call will prompt\n", err)
		cfg = &config.Config{}
	}

	// ONE reader over opts.In, shared with the approver: two bufio.Readers over
	// the same stream would have the first buffer ahead and swallow the approval
	// line. Safe because the agent blocks on the tool call — the user is
	// answering a prompt, not typing a new message.
	reader := bufio.NewReader(opts.In)
	pipeline := &localtool.Pipeline{
		Registry: reg,
		Policy:   localtool.NewPolicy(cfg.Chat.Permissions),
		Approver: &localtool.TerminalApprover{In: reader, Out: opts.Out},
		OnAllowAlways: func(p *localtool.Policy) {
			// Write back ONLY the allow rules. Assigning the whole
			// Permissions struct would clobber tier defaults the user
			// edited mid-session (e.g. flipping read="deny") with this
			// session's stale copy.
			c, err := config.Load()
			if err == nil {
				c.Chat.Permissions.Allow = p.Permissions().Allow
				err = c.Save()
			}
			if err != nil {
				// A rule that silently failed to persist would have the user
				// re-approving the same file every session with no idea why.
				fmt.Fprintf(opts.Out, "warning: could not save the \"allow always\" rule: %v\n", err)
			}
		},
	}
	// Without a config directory there is nowhere the audit log belongs; writing
	// it to a CWD-relative path would scatter security records across whatever
	// directory the user happened to start in.
	if opts.ConfigDir != "" {
		pipeline.Audit = &localtool.FileAudit{Path: filepath.Join(opts.ConfigDir, "chat-audit.jsonl")}
	}

	sessionID := uuid.NewString()
	if err := cli.Send(chat.Hello{
		SessionID:     sessionID,
		ClientVersion: Version,
		Capabilities:  chat.Capabilities{LocalTools: reg.Names()},
		Conversation:  map[string]any{"new": true},
	}); err != nil {
		return err
	}

	for ev := range cli.Events() {
		switch ev.Kind {
		case chat.KindServerHello:
			pipeline.ConversationID = ev.ServerHello.ConversationID
			fmt.Fprint(opts.Out, "> ")
			line, err := reader.ReadString('\n')
			if err != nil && line == "" {
				return nil // EOF with nothing typed: nothing to send
			}
			if err := cli.Send(chat.Chat{SessionID: sessionID, Text: line}); err != nil {
				return err
			}

		case chat.KindChatStream:
			switch ev.ChatStream.Event {
			case "text.delta":
				if d, ok := ev.ChatStream.Data["delta"].(string); ok {
					fmt.Fprint(opts.Out, d)
				}
			case "turn.done":
				fmt.Fprintln(opts.Out)
				return nil // skeleton: one turn per session
			case "error":
				fmt.Fprintf(opts.Out, "\n[error] %v\n", ev.ChatStream.Data["message"])
				return nil
			}

		case chat.KindMcpCall:
			res := pipeline.Handle(ev.McpCall)
			if err := cli.Send(res); err != nil {
				return err
			}

		case chat.KindError:
			return ev.Err

		case chat.KindClosed:
			return nil
		}
	}
	return nil
}
