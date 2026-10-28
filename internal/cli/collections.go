package cli

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"arpegio/internal/api"
	"arpegio/internal/client"
)

func addCollectionCommands(root *cobra.Command, c func() *client.Client) {
	root.AddCommand(playlistCmd(c), likeCmd(c, "like", "PUT", "Like a track, album or artist"),
		likeCmd(c, "unlike", "DELETE", "Remove a like"), likesCmd(c), historyCmd(c), statsCmd(c), homeCmd(c))
}

func playlistCmd(c func() *client.Client) *cobra.Command {
	show := func(cmd *cobra.Command, p api.PlaylistDTO) {
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "%s  %s (%d tracks)\n", p.ID, p.Name, p.ItemCount)
		for _, it := range p.Items {
			fmt.Fprintf(w, "  %-8v %s\n", it.Position, trackLine(it.Track))
		}
	}
	pl := &cobra.Command{Use: "playlist", Short: "Manage playlists"}
	pl.AddCommand(
		&cobra.Command{
			Use: "list", Short: "List playlists", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				var res struct{ Items []api.PlaylistDTO }
				if err := c().Do(cmd.Context(), "GET", "/playlists", nil, &res); err != nil {
					return err
				}
				if len(res.Items) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "no playlists")
				}
				for _, p := range res.Items {
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %s (%d tracks)\n", p.ID, p.Name, p.ItemCount)
				}
				return nil
			},
		},
		&cobra.Command{
			Use: "create <name>", Short: "Create a playlist", Args: cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				var p api.PlaylistDTO
				if err := c().Do(cmd.Context(), "POST", "/playlists", map[string]string{"name": strings.Join(args, " ")}, &p); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "created %s (%s)\n", p.Name, p.ID)
				return nil
			},
		},
		&cobra.Command{
			Use: "show <id>", Short: "Show a playlist's tracks", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				var p api.PlaylistDTO
				if err := c().Do(cmd.Context(), "GET", "/playlists/"+args[0], nil, &p); err != nil {
					return err
				}
				show(cmd, p)
				return nil
			},
		},
		&cobra.Command{
			Use: "add <id> <track-id>...", Short: "Add tracks to a playlist", Args: cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				var p api.PlaylistDTO
				if err := c().Do(cmd.Context(), "POST", "/playlists/"+args[0]+"/items", map[string]any{"track_ids": args[1:]}, &p); err != nil {
					return err
				}
				show(cmd, p)
				return nil
			},
		},
		&cobra.Command{
			Use: "remove <id> <position>", Short: "Remove the item at a position", Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				var pos float64
				if _, err := fmt.Sscan(args[1], &pos); err != nil {
					return fmt.Errorf("remove: %q is not a position", args[1])
				}
				var p api.PlaylistDTO
				if err := c().Do(cmd.Context(), "DELETE", "/playlists/"+args[0]+"/items", map[string]float64{"position": pos}, &p); err != nil {
					return err
				}
				show(cmd, p)
				return nil
			},
		},
		&cobra.Command{
			Use: "delete <id>", Short: "Delete a playlist", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c().Do(cmd.Context(), "DELETE", "/playlists/"+args[0], nil, nil)
			},
		},
	)
	return pl
}

func likeCmd(c func() *client.Client, use, method, short string) *cobra.Command {
	return &cobra.Command{
		Use: use + " <track|album|artist> <id>", Short: short, Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c().Do(cmd.Context(), method, "/likes/"+args[0]+"/"+args[1], nil, nil)
		},
	}
}

func likesCmd(c func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use: "likes [track|album|artist]", Short: "List favourites (tracks by default)", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := "track"
			if len(args) == 1 {
				kind = args[0]
			}
			var res struct {
				Items []struct {
					ID, Title, Name, Artist, Album string
				}
			}
			if err := c().Do(cmd.Context(), "GET", "/likes?kind="+url.QueryEscape(kind), nil, &res); err != nil {
				return err
			}
			if len(res.Items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing liked yet")
			}
			for _, it := range res.Items {
				name := it.Title
				if name == "" {
					name = it.Name
				}
				if it.Artist != "" {
					name += " — " + it.Artist
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", it.ID, name)
			}
			return nil
		},
	}
}

func historyCmd(c func() *client.Client) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use: "history", Short: "Show recent plays", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res struct{ Items []api.HistoryEntryDTO }
			if err := c().Do(cmd.Context(), "GET", fmt.Sprintf("/history?limit=%d", limit), nil, &res); err != nil {
				return err
			}
			if len(res.Items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing played yet")
			}
			for _, e := range res.Items {
				mark := "✓"
				if e.Skipped {
					mark = "⤼"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s  %s  (%s)\n", mark, e.StartedAt, trackLine(e.Track), clock(e.ListenedMS))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "how many plays to show")
	return cmd
}

func statsCmd(c func() *client.Client) *cobra.Command {
	var rng string
	cmd := &cobra.Command{
		Use: "stats", Short: "Listening statistics", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var s api.StatsDTO
			if err := c().Do(cmd.Context(), "GET", "/stats?range="+url.QueryEscape(rng), nil, &s); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "last %s: %d plays, %.1f hours listened\n", s.Range, s.Plays, float64(s.ListenedMS)/3.6e6)
			for _, sec := range []struct {
				title string
				items []api.CountDTO
			}{{"Top tracks", s.TopTracks}, {"Top artists", s.TopArtists}, {"Top genres", s.TopGenres}} {
				if len(sec.items) == 0 {
					continue
				}
				fmt.Fprintf(w, "\n%s\n", sec.title)
				for i, it := range sec.items {
					fmt.Fprintf(w, "  %2d. %s (%d)\n", i+1, it.Name, it.Plays)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rng, "range", "30d", "time window: 7d, 30d, 12h, all")
	return cmd
}

func homeCmd(c func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use: "home", Short: "Recently played, most played and recently added", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var h api.HomeDTO
			if err := c().Do(cmd.Context(), "GET", "/home", nil, &h); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			for _, sec := range []struct {
				title  string
				tracks []api.TrackDTO
			}{{"Recently played", h.RecentlyPlayed}, {"Most played", h.MostPlayed}, {"Recently added", h.RecentlyAdded}} {
				fmt.Fprintf(w, "%s\n", sec.title)
				if len(sec.tracks) == 0 {
					fmt.Fprintln(w, "  —")
				}
				for _, t := range sec.tracks {
					fmt.Fprintf(w, "  %s  %s\n", t.ID, trackLine(t))
				}
			}
			return nil
		},
	}
}
