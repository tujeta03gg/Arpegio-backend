// Package queue holds the play queue: order, shuffle, repeat and "play next".
//
// Items are ordered by a REAL position so inserts and moves never renumber the rest.
// The queue never reads the catalog; it only stores track ids.
package queue

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"

	"arpegio/internal/apperr"
	"arpegio/internal/events"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

const minGap = 1e-6 // below this, neighbours are renumbered before inserting between them

type Item struct {
	Position float64
	TrackID  string
	Source   string // user, album, playlist, radio
	SourceID string
	Priority int // 1 = "play next"
}

type State struct {
	Items      []Item
	CurrentPos *float64 // nil when nothing is selected
	Shuffle    bool
	Repeat     string // off, all, one
}

// Current returns the selected item, if any.
func (s State) Current() (Item, bool) {
	if s.CurrentPos == nil {
		return Item{}, false
	}
	for _, it := range s.Items {
		if it.Position == *s.CurrentPos {
			return it, true
		}
	}
	return Item{}, false
}

type Service struct {
	st      *store.Store
	bus     *events.Bus
	newSeed func() int64
}

func New(st *store.Store, bus *events.Bus) *Service {
	return &Service{st: st, bus: bus, newSeed: func() int64 { return int64(rand.Uint64() >> 1) }}
}

// ---- reads ----

func (s *Service) State(ctx context.Context) (State, error) {
	return load(ctx, db.New(s.st.R))
}

func load(ctx context.Context, q *db.Queries) (State, error) {
	rows, err := q.ListQueueItems(ctx)
	if err != nil {
		return State{}, fmt.Errorf("queue: list: %w", err)
	}
	cs, err := q.GetQueueState(ctx)
	if err != nil {
		return State{}, fmt.Errorf("queue: state: %w", err)
	}
	st := State{Shuffle: cs.Shuffle != 0, Repeat: cs.RepeatMode}
	if cs.CurrentPos.Valid {
		st.CurrentPos = &cs.CurrentPos.Float64
	}
	for _, r := range rows {
		st.Items = append(st.Items, Item{Position: r.Position, TrackID: r.TrackID, Source: r.Source, SourceID: r.SourceID.String, Priority: int(r.Priority)})
	}
	return st, nil
}

// Remaining counts the items after the current one.
func (s *Service) Remaining(ctx context.Context) (int, error) {
	st, err := s.State(ctx)
	if err != nil {
		return 0, err
	}
	i := indexOf(st)
	if i < 0 {
		return len(st.Items), nil
	}
	return len(st.Items) - i - 1, nil
}

// Peek returns what an automatic advance would play next, without moving.
func (s *Service) Peek(ctx context.Context) (Item, bool, error) {
	st, err := s.State(ctx)
	if err != nil {
		return Item{}, false, err
	}
	it, ok := nextOf(st, true)
	return it, ok, nil
}

func indexOf(st State) int {
	if st.CurrentPos == nil {
		return -1
	}
	for i, it := range st.Items {
		if it.Position == *st.CurrentPos {
			return i
		}
	}
	return -1
}

// nextOf is the single definition of "what comes next". auto is true when a track ended by itself.
func nextOf(st State, auto bool) (Item, bool) {
	if len(st.Items) == 0 {
		return Item{}, false
	}
	i := indexOf(st)
	switch {
	case i < 0:
		return st.Items[0], true
	case auto && st.Repeat == "one":
		return st.Items[i], true
	case i+1 < len(st.Items):
		return st.Items[i+1], true
	case st.Repeat == "all" || (!auto && st.Repeat == "one"):
		return st.Items[0], true
	}
	return Item{}, false
}

// ---- writes ----

// tx runs fn in a write transaction; on success it announces queue.changed.
func (s *Service) tx(ctx context.Context, fn func(q *db.Queries) error) error {
	err := s.st.Tx(ctx, func(tx *sql.Tx) error { return fn(db.New(tx)) })
	if err != nil {
		return err
	}
	s.bus.Publish(events.Event{Type: "queue.changed"})
	return nil
}

// Replace swaps the context part of the queue for ids and selects ids[start].
// "Play next" items that have not played yet are kept, right after the selected track.
func (s *Service) Replace(ctx context.Context, ids []string, source, sourceID string, start int) (Item, error) {
	if len(ids) == 0 || start < 0 || start >= len(ids) {
		return Item{}, apperr.Invalid("queue: nothing to play (start %d of %d)", start, len(ids))
	}
	var cur Item
	err := s.tx(ctx, func(q *db.Queries) error {
		st, err := load(ctx, q)
		if err != nil {
			return err
		}
		var keep []Item
		for _, it := range st.Items {
			if it.Priority == 1 && (st.CurrentPos == nil || it.Position > *st.CurrentPos) {
				keep = append(keep, it)
			}
		}

		var before, after []string
		seed := sql.NullInt64{}
		if st.Shuffle {
			cs, _ := q.GetQueueState(ctx)
			seed = cs.ShuffleSeed
			if !seed.Valid {
				seed = sql.NullInt64{Int64: s.newSeed(), Valid: true}
				if err := q.SetShuffleState(ctx, db.SetShuffleStateParams{Shuffle: 1, ShuffleSeed: seed}); err != nil {
					return fmt.Errorf("queue: save seed: %w", err)
				}
			}
			rest := append(append([]string{}, ids[:start]...), ids[start+1:]...)
			shuffle(rest, seed.Int64)
			after = rest
		} else {
			before, after = ids[:start], ids[start+1:]
		}

		var out []Item
		mk := func(id string, prio int) Item {
			return Item{TrackID: id, Source: source, SourceID: sourceID, Priority: prio}
		}
		for _, id := range before {
			out = append(out, mk(id, 0))
		}
		out = append(out, mk(ids[start], 0))
		curIdx := len(out) - 1
		out = append(out, keep...)
		for _, id := range after {
			out = append(out, mk(id, 0))
		}
		cur, err = rewrite(ctx, q, out, curIdx)
		return err
	})
	return cur, err
}

// rewrite replaces the whole queue with items (renumbered 1..n) and selects items[curIdx] (-1 = none).
func rewrite(ctx context.Context, q *db.Queries, items []Item, curIdx int) (Item, error) {
	if err := q.ClearQueueItems(ctx); err != nil {
		return Item{}, fmt.Errorf("queue: clear: %w", err)
	}
	var cur Item
	for i := range items {
		items[i].Position = float64(i + 1)
		if err := insert(ctx, q, items[i]); err != nil {
			return Item{}, err
		}
		if i == curIdx {
			cur = items[i]
		}
	}
	pos := sql.NullFloat64{}
	if curIdx >= 0 {
		pos = sql.NullFloat64{Float64: cur.Position, Valid: true}
	}
	if err := q.SetCurrentPos(ctx, pos); err != nil {
		return Item{}, fmt.Errorf("queue: set current: %w", err)
	}
	return cur, nil
}

func insert(ctx context.Context, q *db.Queries, it Item) error {
	err := q.InsertQueueItem(ctx, db.InsertQueueItemParams{
		Position: it.Position, TrackID: it.TrackID, Source: it.Source,
		SourceID: sql.NullString{String: it.SourceID, Valid: it.SourceID != ""}, Priority: int64(it.Priority),
	})
	if err != nil {
		return fmt.Errorf("queue: insert %s: %w", it.TrackID, err)
	}
	return nil
}

func shuffle[T any](items []T, seed int64) {
	r := rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
	r.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
}

// Add appends tracks to the end, or right after the current track when next is true.
func (s *Service) Add(ctx context.Context, ids []string, next bool) error {
	return s.add(ctx, ids, next, "user", "")
}

// Extend appends tracks to the end tagged with their origin (used by radio to keep the queue full).
func (s *Service) Extend(ctx context.Context, ids []string, source, sourceID string) error {
	return s.add(ctx, ids, false, source, sourceID)
}

func (s *Service) add(ctx context.Context, ids []string, next bool, source, sourceID string) error {
	if len(ids) == 0 {
		return apperr.Invalid("queue: no tracks to add")
	}
	return s.tx(ctx, func(q *db.Queries) error {
		st, err := load(ctx, q)
		if err != nil {
			return err
		}
		n := float64(len(ids))
		lo, hi, prio := 0.0, 0.0, 0
		if next {
			prio = 1
			if lo, hi = nextSlot(st, len(ids)); hi-lo < minGap*(n+1) {
				if st, err = renumber(ctx, q, st); err != nil {
					return err
				}
				lo, hi = nextSlot(st, len(ids))
			}
		} else if len(st.Items) > 0 {
			lo = st.Items[len(st.Items)-1].Position
			hi = lo + n + 1
		} else {
			hi = n + 1
		}
		step := (hi - lo) / (n + 1)
		for k, id := range ids {
			if err := insert(ctx, q, Item{Position: lo + step*float64(k+1), TrackID: id, Source: source, SourceID: sourceID, Priority: prio}); err != nil {
				return err
			}
		}
		return nil
	})
}

// nextSlot is the open interval where "play next" items go: after the current item and
// after any "play next" items already waiting behind it.
func nextSlot(st State, n int) (lo, hi float64) {
	i := indexOf(st)
	for i+1 < len(st.Items) && st.Items[i+1].Priority == 1 {
		i++
	}
	if i >= 0 {
		lo = st.Items[i].Position
	}
	if i+1 < len(st.Items) {
		return lo, st.Items[i+1].Position
	}
	return lo, lo + float64(n) + 1
}

// renumber rewrites positions as 1..n keeping order and the current selection.
func renumber(ctx context.Context, q *db.Queries, st State) (State, error) {
	curIdx := indexOf(st)
	if _, err := rewrite(ctx, q, st.Items, curIdx); err != nil {
		return State{}, err
	}
	return load(ctx, q)
}

// Move puts the item at position at list index to (0-based, clamped).
func (s *Service) Move(ctx context.Context, position float64, to int) error {
	return s.tx(ctx, func(q *db.Queries) error {
		st, err := load(ctx, q)
		if err != nil {
			return err
		}
		from := -1
		for i, it := range st.Items {
			if it.Position == position {
				from = i
			}
		}
		if from < 0 {
			return apperr.NotFound(sql.ErrNoRows, fmt.Sprintf("queue: position %v", position))
		}
		moved := st.Items[from]
		rest := append(append([]Item{}, st.Items[:from]...), st.Items[from+1:]...)
		to = max(0, min(to, len(rest)))
		order := append(append(append([]Item{}, rest[:to]...), moved), rest[to:]...)

		cur := indexOf(st)
		curIdx := -1
		if cur >= 0 {
			for i, it := range order {
				if it.Position == st.Items[cur].Position {
					curIdx = i
				}
			}
		}
		return placeMoved(ctx, q, order, to, curIdx)
	})
}

// placeMoved gives order[to] a position between its neighbours, renumbering everything if there is no room.
func placeMoved(ctx context.Context, q *db.Queries, order []Item, to, curIdx int) error {
	lo, hi := 0.0, 0.0
	switch {
	case len(order) == 1:
		return nil
	case to == 0:
		hi = order[1].Position
		lo = hi - 1
	case to == len(order)-1:
		lo = order[to-1].Position
		hi = lo + 1
	default:
		lo, hi = order[to-1].Position, order[to+1].Position
	}
	old := order[to].Position
	if hi-lo < minGap*2 {
		if _, err := rewrite(ctx, q, order, curIdx); err != nil {
			return err
		}
		return nil
	}
	n, err := q.DeleteQueueItem(ctx, old)
	if err != nil || n != 1 {
		return fmt.Errorf("queue: move: delete old position: %v", err)
	}
	it := order[to]
	it.Position = (lo + hi) / 2
	if err := insert(ctx, q, it); err != nil {
		return err
	}
	if curIdx == to {
		return q.SetCurrentPos(ctx, sql.NullFloat64{Float64: it.Position, Valid: true})
	}
	return nil
}

// Remove deletes one item. The selected track cannot be removed.
func (s *Service) Remove(ctx context.Context, position float64) error {
	return s.tx(ctx, func(q *db.Queries) error {
		st, err := load(ctx, q)
		if err != nil {
			return err
		}
		if st.CurrentPos != nil && *st.CurrentPos == position {
			return apperr.Invalid("queue: cannot remove the track that is playing")
		}
		n, err := q.DeleteQueueItem(ctx, position)
		if err != nil {
			return fmt.Errorf("queue: remove: %w", err)
		}
		if n == 0 {
			return apperr.NotFound(sql.ErrNoRows, fmt.Sprintf("queue: position %v", position))
		}
		return nil
	})
}

// Clear empties the queue but keeps the selected track.
func (s *Service) Clear(ctx context.Context) error {
	return s.tx(ctx, func(q *db.Queries) error {
		st, err := load(ctx, q)
		if err != nil {
			return err
		}
		if cur, ok := st.Current(); ok {
			_, err = rewrite(ctx, q, []Item{cur}, 0)
			return err
		}
		_, err = rewrite(ctx, q, nil, -1)
		return err
	})
}

// Next advances the selection. auto is true when the track ended by itself (repeat-one then replays it).
// ok is false at the end of the queue; the selection does not move in that case.
func (s *Service) Next(ctx context.Context, auto bool) (Item, bool, error) {
	var out Item
	var ok bool
	err := s.tx(ctx, func(q *db.Queries) error {
		st, err := load(ctx, q)
		if err != nil {
			return err
		}
		if out, ok = nextOf(st, auto); !ok {
			return nil
		}
		return q.SetCurrentPos(ctx, sql.NullFloat64{Float64: out.Position, Valid: true})
	})
	return out, ok, err
}

// Previous moves the selection back. At the start (without repeat) it returns the current item so the caller restarts it.
func (s *Service) Previous(ctx context.Context) (Item, bool, error) {
	var out Item
	var ok bool
	err := s.tx(ctx, func(q *db.Queries) error {
		st, err := load(ctx, q)
		if err != nil {
			return err
		}
		i := indexOf(st)
		switch {
		case len(st.Items) == 0:
			return nil
		case i > 0:
			out, ok = st.Items[i-1], true
		case i == 0 && st.Repeat == "all":
			out, ok = st.Items[len(st.Items)-1], true
		case i == 0:
			out, ok = st.Items[0], true
		default:
			return nil
		}
		return q.SetCurrentPos(ctx, sql.NullFloat64{Float64: out.Position, Valid: true})
	})
	return out, ok, err
}

// SetShuffle turns shuffle on (reordering what comes after the current track with a saved seed) or off.
// ponytail: turning it off keeps the shuffled order; the original order is not restored.
func (s *Service) SetShuffle(ctx context.Context, on bool) error {
	return s.tx(ctx, func(q *db.Queries) error {
		if !on {
			return q.SetShuffleState(ctx, db.SetShuffleStateParams{Shuffle: 0})
		}
		st, err := load(ctx, q)
		if err != nil {
			return err
		}
		seed := s.newSeed()
		if err := q.SetShuffleState(ctx, db.SetShuffleStateParams{Shuffle: 1, ShuffleSeed: sql.NullInt64{Int64: seed, Valid: true}}); err != nil {
			return fmt.Errorf("queue: save shuffle: %w", err)
		}
		i := indexOf(st)
		head, tail := st.Items[:i+1], st.Items[i+1:]
		// "play next" items stay first; the rest is shuffled
		var prio, rest []Item
		for _, it := range tail {
			if it.Priority == 1 {
				prio = append(prio, it)
			} else {
				rest = append(rest, it)
			}
		}
		shuffle(rest, seed)
		order := append([]Item{}, head...)
		order = append(order, prio...)
		order = append(order, rest...)
		_, err = rewrite(ctx, q, order, i)
		return err
	})
}

func (s *Service) SetRepeat(ctx context.Context, mode string) error {
	if mode != "off" && mode != "all" && mode != "one" {
		return apperr.Invalid("queue: repeat must be off, all or one")
	}
	return s.tx(ctx, func(q *db.Queries) error { return q.SetRepeatMode(ctx, mode) })
}

// Select makes the item at position the current one (jumping to it from "up next" or anywhere in the list).
func (s *Service) Select(ctx context.Context, position float64) (Item, error) {
	var out Item
	err := s.tx(ctx, func(q *db.Queries) error {
		st, err := load(ctx, q)
		if err != nil {
			return err
		}
		for _, it := range st.Items {
			if it.Position == position {
				out = it
				return q.SetCurrentPos(ctx, sql.NullFloat64{Float64: position, Valid: true})
			}
		}
		return apperr.NotFound(sql.ErrNoRows, fmt.Sprintf("queue: position %v", position))
	})
	return out, err
}
