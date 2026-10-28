package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"arpegio/internal/api"
	"arpegio/internal/client"
)

func addExtraCommands(root *cobra.Command, c func() *client.Client) {
	root.AddCommand(
		&cobra.Command{
			Use: "radio <track:ID|artist:ID|genre:ID>", Short: "Start an endless radio from a seed", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return playerCall(cmd, c(), "POST", "/radio", map[string]string{"seed": args[0]})
			},
		},
		&cobra.Command{
			Use: "lyrics <track-id>", Short: "Show a track's lyrics", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				var l api.LyricsDTO
				if err := c().Do(cmd.Context(), "GET", "/tracks/"+args[0]+"/lyrics", nil, &l); err != nil {
					return err
				}
				for _, ln := range l.Lines {
					if ln.TimeMS != nil {
						fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", clock(*ln.TimeMS), ln.Text)
					} else {
						fmt.Fprintln(cmd.OutOrStdout(), ln.Text)
					}
				}
				return nil
			},
		},
		&cobra.Command{
			Use: "import <file>...", Short: "Upload audio files to the library's import folder", Args: cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				var res struct {
					Imported []api.ImportedDTO
					Errors   []api.ImportedDTO
				}
				if err := c().Upload(cmd.Context(), "/library/import", args, &res); err != nil {
					return err
				}
				w := cmd.OutOrStdout()
				for _, f := range res.Imported {
					fmt.Fprintf(w, "imported %s\n", f.Name)
				}
				for _, f := range res.Errors {
					fmt.Fprintf(w, "skipped %s: %s\n", f.Name, strings.TrimSpace(f.Err))
				}
				return nil
			},
		},
	)
}
