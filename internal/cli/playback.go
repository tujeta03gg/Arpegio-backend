package cli

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"arpegio/internal/api"
	"arpegio/internal/client"
)

// addPlaybackCommands registers the player and queue commands.
func addPlaybackCommands(root *cobra.Command, c func() *client.Client) {
	// act posts to the player and prints the resulting state.
	act := func(use, short, method, path string) *cobra.Command {
		return &cobra.Command{
			Use: use, Short: short, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return playerCall(cmd, c(), method, path, nil) },
		}
	}

	play := &cobra.Command{
		Use:   "play [context]",
		Short: "Resume, or play a context such as album:ID, playlist:ID or artist:ID",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]string{}
			if len(args) == 1 {
				body["context"] = args[0]
			}
			if s, _ := cmd.Flags().GetString("start"); s != "" {
				body["start"] = s
			}
			return playerCall(cmd, c(), "POST", "/player/play", body)
		},
	}
	play.Flags().String("start", "", "track to start with, e.g. track:ID")

	root.AddCommand(
		play,
		act("pause", "Pause playback", "POST", "/player/pause"),
		act("next", "Skip to the next track", "POST", "/player/next"),
		act("previous", "Go back to the previous track", "POST", "/player/previous"),
		&cobra.Command{
			Use: "status", Short: "Show what is playing", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return playerCall(cmd, c(), "GET", "/player", nil) },
		},
		&cobra.Command{
			Use: "seek <seconds>", Short: "Jump to a position in the current track", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				secs, err := strconv.ParseFloat(args[0], 64)
				if err != nil || secs < 0 {
					return fmt.Errorf("seek: %q is not a valid number of seconds", args[0])
				}
				return playerCall(cmd, c(), "PUT", "/player/position", map[string]int64{"ms": int64(secs * 1000)})
			},
		},
		&cobra.Command{
			Use: "volume <0-100>", Short: "Set the volume", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				n, err := strconv.Atoi(args[0])
				if err != nil {
					return fmt.Errorf("volume: %q is not a number", args[0])
				}
				return playerCall(cmd, c(), "PUT", "/player/volume", map[string]int{"level": n})
			},
		},
		&cobra.Command{
			Use: "shuffle <on|off>", Short: "Turn shuffle on or off", Args: cobra.ExactArgs(1), ValidArgs: []string{"on", "off"},
			RunE: func(cmd *cobra.Command, args []string) error {
				if args[0] != "on" && args[0] != "off" {
					return fmt.Errorf("shuffle: use on or off")
				}
				return playerCall(cmd, c(), "PUT", "/player/shuffle", map[string]bool{"enabled": args[0] == "on"})
			},
		},
		&cobra.Command{
			Use: "repeat <off|all|one>", Short: "Set the repeat mode", Args: cobra.ExactArgs(1), ValidArgs: []string{"off", "all", "one"},
			RunE: func(cmd *cobra.Command, args []string) error {
				return playerCall(cmd, c(), "PUT", "/player/repeat", map[string]string{"mode": args[0]})
			},
		},
		queueCmd(c),
	)
}

func playerCall(cmd *cobra.Command, c *client.Client, method, path string, body any) error {
	var p api.PlayerDTO
	if err := c.Do(cmd.Context(), method, path, body, &p); err != nil {
		return err
	}
	printPlayer(cmd.OutOrStdout(), p)
	return nil
}

func printPlayer(w io.Writer, p api.PlayerDTO) {
	if p.Track == nil {
		fmt.Fprintf(w, "stopped  vol %d  shuffle %s  repeat %s\n", p.Volume, onOff(p.Shuffle), p.Repeat)
		return
	}
	icon := "⏸"
	if p.Playing {
		icon = "▶"
	}
	fmt.Fprintf(w, "%s %s\n  %s / %s  vol %d  shuffle %s  repeat %s\n",
		icon, trackLine(*p.Track), clock(p.PositionMS), clock(p.DurationMS), p.Volume, onOff(p.Shuffle), p.Repeat)
}

func trackLine(t api.TrackDTO) string {
	s := t.Title
	if t.Artist != "" {
		s += " — " + t.Artist
	}
	if t.Album != "" {
		s += " (" + t.Album + ")"
	}
	return s
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func clock(ms int64) string {
	s := ms / 1000
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func queueCmd(c func() *client.Client) *cobra.Command {
	show := func(cmd *cobra.Command, method, path string, body any) error {
		var q api.QueueDTO
		if err := c().Do(cmd.Context(), method, path, body, &q); err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		if len(q.Items) == 0 {
			fmt.Fprintln(w, "queue is empty")
			return nil
		}
		for _, it := range q.Items {
			mark := " "
			if it.Current {
				mark = "▶"
			}
			next := ""
			if it.Priority == 1 {
				next = "  [next]"
			}
			fmt.Fprintf(w, "%s %-8v %s%s\n", mark, it.Position, trackLine(it.Track), next)
		}
		return nil
	}

	q := &cobra.Command{
		Use: "queue", Short: "Show the play queue", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return show(cmd, "GET", "/queue", nil) },
	}
	add := &cobra.Command{
		Use: "add <track-id|context>...", Short: "Add tracks (or album:ID, playlist:ID...) to the queue", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			next, _ := cmd.Flags().GetBool("next")
			body := map[string]any{"next": next}
			var ids []string
			for _, a := range args {
				if isContext(a) {
					body["context"] = a // the API takes one context per call
					continue
				}
				ids = append(ids, a)
			}
			if len(ids) > 0 {
				body["track_ids"] = ids
			}
			return show(cmd, "POST", "/queue", body)
		},
	}
	add.Flags().Bool("next", false, "play right after the current track")
	q.AddCommand(add,
		&cobra.Command{
			Use: "remove <position>", Short: "Remove an item by its position", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error { return show(cmd, "DELETE", "/queue/"+args[0], nil) },
		},
		&cobra.Command{
			Use: "move <position> <index>", Short: "Move an item to a list index (0 = top)", Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				idx, err := strconv.Atoi(args[1])
				if err != nil {
					return fmt.Errorf("move: %q is not an index", args[1])
				}
				return show(cmd, "PATCH", "/queue/"+args[0], map[string]int{"index": idx})
			},
		},
		&cobra.Command{
			Use: "clear", Short: "Clear the queue (the playing track stays)", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return show(cmd, "DELETE", "/queue", nil) },
		},
	)
	return q
}

// isContext tells "album:ID" from a bare track id (ULIDs never contain a colon).
func isContext(s string) bool {
	for _, r := range s {
		if r == ':' {
			return true
		}
	}
	return false
}
