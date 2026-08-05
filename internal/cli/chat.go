package cli

import (
	"bufio"
	"context"
	"errors"
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

	// Ctrl-C has to end the session, and nothing else here would let it.
	// chat.Dial deliberately detaches the client's internal context from ctx, so
	// a cancelled ctx leaves this loop waiting on a connection nobody is going to
	// answer — and pressing Ctrl-C again does not help: signal.NotifyContext
	// keeps its handler installed for the life of the process, so every later
	// SIGINT lands in an already-full 1-deep channel instead of reaching the
	// default kill behaviour. The process would simply be unkillable from the
	// keyboard. Closing the client on cancel makes readLoop fail, which closes
	// Events() and unwinds the loop below.
	//
	// Residual, deliberately not closed here: a cancel arriving while we are
	// blocked reading a line from stdin goes unnoticed until that read returns,
	// because a blocking read on os.Stdin cannot be cancelled. The fix for that
	// is raw-mode input in the rich TUI.
	stop := context.AfterFunc(ctx, cli.Close)
	defer stop()

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
		// Same treatment as every other send: a Ctrl-C landing in the
		// Dial -> Hello window (which config.Load's disk I/O widens) would
		// otherwise escape as a raw "context canceled" and exit 1.
		return sendErr(ctx, opts.Out, err)
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
				return sendErr(ctx, opts.Out, err)
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
				return sendErr(ctx, opts.Out, err)
			}

		case chat.KindError:
			return ev.Err

		case chat.KindClosed:
			closedNotice(ctx, opts.Out, ev.Err)
			return nil
		}
	}
	return nil
}

// sendErr maps a failed send to what the user should see. A connection that
// dropped mid-turn races Send: the client's send buffer still has room while
// its context is already cancelled, so Go picks either case at random and the
// SAME failure would otherwise exit 0 in silence or exit 1 with a bare
// "context canceled". Both halves of that coin now converge — the drop is
// reported once, on the way to a zero exit — whichever way the select lands.
func sendErr(ctx context.Context, out io.Writer, err error) error {
	if errors.Is(err, context.Canceled) {
		closedNotice(ctx, out, nil)
		return nil
	}
	return err
}

// closedNotice tells the user the connection ended. Silence here reads as a
// finished turn — a drop mid-approval would otherwise exit 0 having printed
// nothing at all. An ordinary close (clean handshake, EOF, or the cancel-driven
// Close above) needs no detail; anything else carries its reason.
//
// A user who pressed Ctrl-C is the exception: they already know, and telling
// them anyway would be a coin flip rather than a message — once the client's
// context is cancelled, the event carrying the close races the cancellation and
// arrives only sometimes. Keying off the caller's ctx makes both the silence
// (cancelled) and the notice (any real drop) deterministic.
func closedNotice(ctx context.Context, out io.Writer, err error) {
	if ctx.Err() != nil {
		return
	}
	if chat.IsExpectedClose(err) {
		fmt.Fprintln(out, "\n[connection closed]")
		return
	}
	fmt.Fprintf(out, "\n[connection closed: %v]\n", err)
}
