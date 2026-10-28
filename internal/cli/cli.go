// Package cli builds the `arpegio` command tree. It only talks to the daemon through client.
package cli

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"arpegio/internal/api"
	"arpegio/internal/client"
	"arpegio/internal/tui"
)

// New returns the root command. addr is the default daemon address; out receives all output.
func New(addr string, out io.Writer) *cobra.Command {
	var c *client.Client
	root := &cobra.Command{
		Use:           "arpegio",
		Short:         "Control the Arpegio music daemon",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			a, _ := cmd.Flags().GetString("addr")
			c = client.New(a)
		},
	}
	root.PersistentFlags().String("addr", addr, "daemon address (http://host:port or unix:///path)")
	root.SetOut(out)
	root.SetErr(out)

	get := func() *client.Client { return c }
	root.AddCommand(scanCmd(get), searchCmd(get), libraryCmd(get))
	addPlaybackCommands(root, get)
	addCollectionCommands(root, get)
	addExtraCommands(root, get)
	root.AddCommand(tuiCmd(get))
	return root
}

func scanCmd(c func() *client.Client) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan the library folders for new, changed or removed files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := c().Do(ctx, "POST", "/library/scan", nil, nil); err != nil {
				return err
			}
			if !wait {
				fmt.Fprintln(cmd.OutOrStdout(), "scan started")
				return nil
			}
			// ponytail: the daemon starts the scan in a goroutine; give it a moment to flag itself as scanning.
			time.Sleep(300 * time.Millisecond)
			for {
				var st api.StatusDTO
				if err := c().Do(ctx, "GET", "/library/status", nil, &st); err != nil {
					return err
				}
				if !st.Scanning {
					fmt.Fprintln(cmd.OutOrStdout(), "scan finished")
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "scanning %d/%d\n", st.Done, st.Total)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(500 * time.Millisecond):
				}
			}
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "wait until the scan finishes")
	return cmd
}

func searchCmd(c func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "search <text>",
		Short: "Search tracks, albums and artists",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res map[string][]api.HitDTO
			q := url.QueryEscape(strings.Join(args, " "))
			if err := c().Do(cmd.Context(), "GET", "/search?q="+q, nil, &res); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			found := false
			for _, kind := range []string{"artists", "albums", "tracks"} {
				for _, h := range res[kind] {
					found = true
					fmt.Fprintf(w, "%-6s %s  %s\n", kind[:len(kind)-1], h.ID, describe(h))
				}
			}
			if !found {
				fmt.Fprintln(w, "no results")
			}
			return nil
		},
	}
}

func describe(h api.HitDTO) string {
	s := h.Title
	if h.Artist != "" {
		s += " — " + h.Artist
	}
	if h.Album != "" && h.Album != h.Title {
		s += " (" + h.Album + ")"
	}
	return s
}

func libraryCmd(c func() *client.Client) *cobra.Command {
	lib := &cobra.Command{Use: "library", Short: "Manage watched folders"}
	lib.AddCommand(
		&cobra.Command{
			Use: "list", Short: "List watched folders", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				var res struct{ Items []api.RootDTO }
				if err := c().Do(cmd.Context(), "GET", "/library/roots", nil, &res); err != nil {
					return err
				}
				for _, r := range res.Items {
					state := "on"
					if !r.Enabled {
						state = "off"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %-3s %s\n", r.ID, state, r.Path)
				}
				return nil
			},
		},
		&cobra.Command{
			Use: "add <path>", Short: "Watch a folder", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				var r api.RootDTO
				if err := c().Do(cmd.Context(), "POST", "/library/roots", map[string]string{"path": args[0]}, &r); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "watching %s (%s)\n", r.Path, r.ID)
				return nil
			},
		},
		&cobra.Command{
			Use: "remove <id>", Short: "Stop watching a folder", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c().Do(cmd.Context(), "DELETE", "/library/roots/"+args[0], nil, nil)
			},
		},
	)
	return lib
}

func tuiCmd(c func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use: "tui", Short: "Open the interactive terminal interface", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return tui.Run(cmd.Context(), c()) },
	}
}
