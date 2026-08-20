// user.go implements the `ladym user` subcommand group: local management of
// the users table backing the HTTP data-plane's Basic auth. The commands talk
// to the local store directly (same engine path as the other data commands) —
// user management never goes through HTTP, so it works before auth is
// bootstrapped.
//
// Passwords are never accepted as command-line arguments or printed/logged:
// they come from an interactive two-prompt read (TTY only) or from
// --password-env VAR indirection, and only the bcrypt hash is persisted.
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
)

func userCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage users for the HTTP data-plane's Basic auth (local store).",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(userAddCmd(), userListCmd(), userDeleteCmd(), userPasswdCmd())
	return cmd
}

// readPasswordTwice reads two newline-terminated password entries from r and
// requires them to match (the interactive confirm-prompt path). prompt is
// written before each read when non-nil.
func readPasswordTwice(r io.Reader, prompt io.Writer) (string, error) {
	br := bufio.NewReader(r)
	read := func(label string) (string, error) {
		if prompt != nil {
			fmt.Fprintf(prompt, "%s: ", label)
		}
		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	first, err := read("Password")
	if err != nil {
		return "", err
	}
	second, err := read("Confirm password")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", &config.ConfigError{Msg: "passwords do not match"}
	}
	return first, nil
}

// stdinIsTTY reports whether the interactive password prompt is possible.
// A variable so tests can force the no-TTY path.
var stdinIsTTY = func() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// resolveNewPassword obtains the password for add/passwd without ever taking
// it from argv: --password-env VAR reads the named env var (an explicitly set
// empty value creates/keeps a passwordless user); otherwise an interactive
// two-prompt read, which requires a TTY.
func resolveNewPassword(passwordEnv string) (string, error) {
	if passwordEnv != "" {
		pw, ok := os.LookupEnv(passwordEnv)
		if !ok {
			return "", &config.ConfigError{Msg: fmt.Sprintf("--password-env %s is not set; export it or drop the flag for an interactive prompt", passwordEnv)}
		}
		return pw, nil
	}
	if !stdinIsTTY() {
		return "", &config.ConfigError{Msg: "no TTY for the interactive password prompt; use --password-env VAR instead"}
	}
	return readPasswordTwice(os.Stdin, os.Stderr)
}

// hashPassword bcrypts pw; "" stays "" (passwordless user).
func hashPassword(pw string) (string, error) {
	if pw == "" {
		return "", nil
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// userStore opens the local engine the user commands operate on.
func userStore(db string) (*engine.Engine, error) {
	return newEngine(db, "")
}

func userAddCmd() *cobra.Command {
	var db, workspace, passwordEnv string
	var admin bool
	cmd := &cobra.Command{
		Use:   "add <username>",
		Short: "Create or update a user (upsert).",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			pw, err := resolveNewPassword(passwordEnv)
			if err != nil {
				return err
			}
			hash, err := hashPassword(pw)
			if err != nil {
				return err
			}
			eng, err := userStore(db)
			if err != nil {
				return err
			}
			defer eng.Close()
			u := &schema.User{
				Username: args[0], PasswordHash: hash, Workspace: workspace,
				Admin: admin, CreatedAt: schema.Now(),
			}
			if err := eng.Store.PutUser(u); err != nil {
				return err
			}
			fmt.Printf("added user %s (workspace=%s, admin=%t)\n", u.Username, printableWS(u.Workspace), u.Admin)
			return nil
		},
	}
	cmd.Flags().StringVar(&db, "db", "", "Path to ladym.db")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace forced on this user (non-admin)")
	cmd.Flags().BoolVar(&admin, "admin", false, "Grant admin (no workspace forcing; manages users)")
	cmd.Flags().StringVar(&passwordEnv, "password-env", "", "Env var holding the password (empty value = passwordless user); without it, an interactive prompt is used")
	return cmd
}

func userListCmd() *cobra.Command {
	var db string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List users (password hashes are never printed).",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			eng, err := userStore(db)
			if err != nil {
				return err
			}
			defer eng.Close()
			users, err := eng.Store.ListUsers()
			if err != nil {
				return err
			}
			if len(users) == 0 {
				fmt.Println("no users")
				return nil
			}
			fmt.Printf("%-24s %-16s %-6s %s\n", "username", "workspace", "admin", "created")
			for _, u := range users {
				fmt.Printf("%-24s %-16s %-6t %s\n",
					u.Username, printableWS(u.Workspace), u.Admin,
					time.Unix(int64(u.CreatedAt), 0).UTC().Format("2006-01-02 15:04:05"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&db, "db", "", "Path to ladym.db")
	return cmd
}

func userDeleteCmd() *cobra.Command {
	var db string
	cmd := &cobra.Command{
		Use:   "delete <username>",
		Short: "Delete a user.",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			eng, err := userStore(db)
			if err != nil {
				return err
			}
			defer eng.Close()
			u, err := eng.Store.GetUser(args[0])
			if err != nil {
				return err
			}
			if u == nil {
				return &config.ConfigError{Msg: "no such user " + args[0]}
			}
			if err := eng.Store.DeleteUser(args[0]); err != nil {
				return err
			}
			fmt.Printf("deleted user %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&db, "db", "", "Path to ladym.db")
	return cmd
}

func userPasswdCmd() *cobra.Command {
	var db, passwordEnv string
	cmd := &cobra.Command{
		Use:   "passwd <username>",
		Short: "Change a user's password (workspace/admin are preserved).",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			pw, err := resolveNewPassword(passwordEnv)
			if err != nil {
				return err
			}
			hash, err := hashPassword(pw)
			if err != nil {
				return err
			}
			eng, err := userStore(db)
			if err != nil {
				return err
			}
			defer eng.Close()
			u, err := eng.Store.GetUser(args[0])
			if err != nil {
				return err
			}
			if u == nil {
				return &config.ConfigError{Msg: "no such user " + args[0]}
			}
			u.PasswordHash = hash
			if err := eng.Store.PutUser(u); err != nil {
				return err
			}
			fmt.Printf("password updated for %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&db, "db", "", "Path to ladym.db")
	cmd.Flags().StringVar(&passwordEnv, "password-env", "", "Env var holding the new password (empty value = make the user passwordless); without it, an interactive prompt is used")
	return cmd
}

// printableWS renders an empty (unset) user workspace for the list/add output.
func printableWS(ws string) string {
	if ws == "" {
		return "-"
	}
	return ws
}
