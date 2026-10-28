package playback

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"arpegio/internal/apperr"
	"arpegio/internal/catalog"
	"arpegio/internal/events"
	"arpegio/internal/history"
	"arpegio/internal/queue"
	"arpegio/internal/store"
	"arpegio/internal/store/db"
)

const (
	restartThresholdMS = 3000 // "previous" restarts the track when it is past this point
	maxLoadFailures    = 3    // consecutive unplayable tracks before the player gives up
	radioLowWater      = 5    // refill radio when fewer tracks than this are left
	radioBatch         = 10
	saveEvery          = 30 * time.Second
)

// Queue is the part of queue.Service playback uses.
type Queue interface {
	Replace(ctx context.Context, ids []string, source, sourceID string, start int) (queue.Item, error)
	Next(ctx context.Context, auto bool) (queue.Item, bool, error)
	Previous(ctx context.Context) (queue.Item, bool, error)
	Peek(ctx context.Context) (queue.Item, bool, error)
	State(ctx context.Context) (queue.State, error)
	Remaining(ctx context.Context) (int, error)
	Extend(ctx context.Context, ids []string, source, sourceID string) error
	SetShuffle(ctx context.Context, on bool) error
	SetRepeat(ctx context.Context, mode string) error
}

// Tracks resolves track metadata and files.
type Tracks interface {
	Track(ctx context.Context, id string) (catalog.Track, error)
	TrackPath(ctx context.Context, id string) (string, error)
}

// Recorder stores finished plays (history.Service).
type Recorder interface {
	Record(ctx context.Context, p history.Play) (bool, error)
}

// Radio supplies more tracks for a radio queue.
type Radio interface {
	More(ctx context.Context, sourceID string, n int) ([]string, error)
}

// Deps are the collaborators of the player.
type Deps struct {
	Engine   Engine
	Queue    Queue
	Tracks   Tracks
	History  Recorder // optional
	Radio    Radio    // optional
	Contexts *Resolver
	Store    *store.Store
	Bus      *events.Bus
	Output   string // audio output name saved with the state
	Log      *slog.Logger
	Now      func() time.Time // defaults to time.Now
}

// State is the full player state served by GET /player.
type State struct {
	TrackID    string
	PositionMS int64
	DurationMS int64
	Playing    bool
	Volume     int
	Shuffle    bool
	Repeat     string
}

type current struct {
	trackID    string
	pos        float64 // queue position of the item
	source     string
	sourceID   string
	durationMS int64
	startedAt  time.Time
	listenedMS int64
	lastPosMS  int64
}

type preloaded struct {
	trackID string
	pos     float64
}

type Service struct {
	Deps
	mu        sync.Mutex
	cur       *current
	pre       *preloaded
	paused    bool
	volume    int
	positionM int64
	failures  int
	lastEmit  time.Time
}

func New(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{Deps: d, volume: 70}
}

// ---- lifecycle ----

// Run processes engine and queue events until ctx ends, saving state periodically and on exit.
func (s *Service) Run(ctx context.Context) {
	qev, cancel := s.Bus.Subscribe(16)
	defer cancel()
	tick := time.NewTicker(saveEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.save(context.Background())
			s.mu.Unlock()
			return
		case ev := <-s.Engine.Events():
			s.handle(ctx, ev)
		case ev := <-qev:
			if ev.Type == "queue.changed" {
				s.mu.Lock()
				s.refreshPreload(ctx)
				s.mu.Unlock()
			}
		case <-tick.C:
			s.mu.Lock()
			if s.cur != nil && !s.paused {
				s.save(ctx)
			}
			s.mu.Unlock()
		}
	}
}

// Restore brings back the saved volume and the selected track (paused, at its saved position)
// after a daemon restart. It only fills the service state; the engine gets it when it
// announces itself with EvRestarted.
func (s *Service) Restore(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := db.New(s.Store.R).GetPlayerState(ctx)
	if err != nil {
		return fmt.Errorf("playback: load state: %w", err)
	}
	s.volume = int(ps.Volume)
	st, err := s.Queue.State(ctx)
	if err != nil {
		return err
	}
	item, ok := st.Current()
	if !ok {
		return nil
	}
	tr, err := s.Tracks.Track(ctx, item.TrackID)
	if err != nil {
		s.Log.Warn("playback: could not restore track", slog.String("track_id", item.TrackID), slog.Any("err", err))
		return nil
	}
	s.setCurrent(item, tr)
	if ps.TrackID.String == item.TrackID {
		s.positionM, s.cur.lastPosMS = ps.PositionMs, ps.PositionMs
	}
	s.paused = true
	s.publishState(ctx)
	return nil
}

// reload puts the engine back in sync after it (re)started: volume, current track and preload.
func (s *Service) reload(ctx context.Context) {
	if err := s.Engine.SetVolume(ctx, s.volume); err != nil {
		s.Log.Warn("playback: set volume", slog.Any("err", err))
	}
	s.pre = nil
	if s.cur == nil {
		return
	}
	path, err := s.Tracks.TrackPath(ctx, s.cur.trackID)
	if err == nil {
		err = s.Engine.Load(ctx, path, s.positionM, s.paused)
	}
	if err != nil {
		s.Log.Warn("playback: reload current track", slog.String("track_id", s.cur.trackID), slog.Any("err", err))
		return
	}
	s.refreshPreload(ctx)
}

// ---- commands ----

// PlayRequest starts a context ("album:ID", "playlist:ID", ...) at an optional track ("track:ID").
// Both empty means resume.
type PlayRequest struct {
	Context string
	Start   string
}

func (s *Service) Play(ctx context.Context, req PlayRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Context == "" && req.Start == "" {
		return s.resume(ctx)
	}
	spec := req.Context
	if spec == "" {
		spec = req.Start
	}
	pc, err := s.Contexts.Resolve(ctx, spec)
	if err != nil {
		return err
	}
	start := 0
	if req.Start != "" {
		id, ok := strings.CutPrefix(req.Start, "track:")
		if !ok {
			return apperr.Invalid("playback: start must look like track:ID")
		}
		start = -1
		for i, t := range pc.TrackIDs {
			if t == id {
				start = i
				break
			}
		}
		if start < 0 {
			return apperr.Invalid("playback: track %s is not in %s", id, spec)
		}
	}
	item, err := s.Queue.Replace(ctx, pc.TrackIDs, pc.Source, pc.SourceID, start)
	if err != nil {
		return err
	}
	return s.startItem(ctx, item)
}

func (s *Service) resume(ctx context.Context) error {
	if s.cur != nil {
		if err := s.Engine.SetPause(ctx, false); err != nil {
			return fmt.Errorf("playback: resume: %w", err)
		}
		s.paused = false
		s.publishState(ctx)
		return nil
	}
	st, err := s.Queue.State(ctx)
	if err != nil {
		return err
	}
	item, ok := st.Current()
	if !ok {
		if item, ok, err = s.Queue.Next(ctx, false); err != nil {
			return err
		}
	}
	if !ok {
		return apperr.Invalid("playback: the queue is empty")
	}
	return s.startItem(ctx, item)
}

func (s *Service) Pause(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil {
		return nil
	}
	if err := s.Engine.SetPause(ctx, true); err != nil {
		return fmt.Errorf("playback: pause: %w", err)
	}
	s.paused = true
	s.save(ctx)
	s.publishState(ctx)
	return nil
}

func (s *Service) Next(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok, err := s.Queue.Next(ctx, false)
	if err != nil {
		return err
	}
	if !ok {
		return s.stop(ctx)
	}
	return s.startItem(ctx, item)
}

func (s *Service) Previous(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur != nil && s.positionM > restartThresholdMS {
		return s.seek(ctx, 0)
	}
	item, ok, err := s.Queue.Previous(ctx)
	if err != nil || !ok {
		return err
	}
	return s.startItem(ctx, item)
}

func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stop(ctx)
}

func (s *Service) Seek(ctx context.Context, ms int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil {
		return apperr.Invalid("playback: nothing is playing")
	}
	return s.seek(ctx, ms)
}

func (s *Service) seek(ctx context.Context, ms int64) error {
	if ms < 0 || (s.cur.durationMS > 0 && ms > s.cur.durationMS) {
		return apperr.Invalid("playback: position %d is outside the track", ms)
	}
	if err := s.Engine.Seek(ctx, ms); err != nil {
		return fmt.Errorf("playback: seek: %w", err)
	}
	s.positionM, s.cur.lastPosMS = ms, ms // a seek is not listening time
	s.publishState(ctx)
	return nil
}

func (s *Service) SetVolume(ctx context.Context, level int) error {
	if level < 0 || level > 100 {
		return apperr.Invalid("playback: volume must be 0-100")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Engine.SetVolume(ctx, level); err != nil {
		return fmt.Errorf("playback: volume: %w", err)
	}
	s.volume = level
	s.save(ctx)
	s.publishState(ctx)
	return nil
}

func (s *Service) SetShuffle(ctx context.Context, on bool) error {
	if err := s.Queue.SetShuffle(ctx, on); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshPreload(ctx)
	s.publishState(ctx)
	return nil
}

func (s *Service) SetRepeat(ctx context.Context, mode string) error {
	if err := s.Queue.SetRepeat(ctx, mode); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshPreload(ctx)
	s.publishState(ctx)
	return nil
}

// State returns a snapshot for GET /player and the WebSocket snapshot.
func (s *Service) State(ctx context.Context) (State, error) {
	qs, err := s.Queue.State(ctx)
	if err != nil {
		return State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{Volume: s.volume, Shuffle: qs.Shuffle, Repeat: qs.Repeat, PositionMS: s.positionM}
	if s.cur != nil {
		st.TrackID, st.DurationMS, st.Playing = s.cur.trackID, s.cur.durationMS, !s.paused
	}
	return st, nil
}

// ---- internals (callers hold s.mu) ----

// startItem finishes the current track (as a skip if needed) and plays item.
func (s *Service) startItem(ctx context.Context, item queue.Item) error {
	s.finishCurrent(ctx, false)
	if err := s.load(ctx, item, 0, false); err != nil {
		return err
	}
	s.failures = 0
	s.published("player.track_started", item.TrackID)
	s.publishState(ctx)
	s.save(ctx)
	return nil
}

// load asks the engine to play item and records it as the current track.
func (s *Service) load(ctx context.Context, item queue.Item, startMS int64, paused bool) error {
	tr, err := s.Tracks.Track(ctx, item.TrackID)
	if err != nil {
		return err
	}
	path, err := s.Tracks.TrackPath(ctx, item.TrackID)
	if err != nil {
		return err
	}
	if err := s.Engine.Load(ctx, path, startMS, paused); err != nil {
		return fmt.Errorf("playback: load %s: %w", item.TrackID, err)
	}
	s.pre = nil // loading replaces the engine playlist
	s.setCurrent(item, tr)
	s.positionM, s.cur.lastPosMS, s.paused = startMS, startMS, paused
	s.refreshPreload(ctx)
	return nil
}

func (s *Service) setCurrent(item queue.Item, tr catalog.Track) {
	s.cur = &current{trackID: item.TrackID, pos: item.Position, source: item.Source, sourceID: item.SourceID,
		durationMS: tr.DurationMS, startedAt: s.Now()}
	s.positionM = 0
}

// finishCurrent writes the history entry for the track that is ending.
func (s *Service) finishCurrent(ctx context.Context, eof bool) {
	c := s.cur
	if c == nil || s.History == nil {
		return
	}
	if rest := c.durationMS - c.lastPosMS; eof && rest > 0 && rest <= 2000 {
		c.listenedMS += rest // the last position tick usually lands just before the end
	}
	_, err := s.History.Record(ctx, history.Play{TrackID: c.trackID, StartedAt: c.startedAt, ListenedMS: c.listenedMS, DurationMS: c.durationMS, Source: c.source})
	if err != nil {
		s.Log.Error("playback: record play", slog.String("track_id", c.trackID), slog.Any("err", err))
	}
}

func (s *Service) stop(ctx context.Context) error {
	s.finishCurrent(ctx, false)
	if err := s.Engine.Stop(ctx); err != nil {
		return fmt.Errorf("playback: stop: %w", err)
	}
	s.endPlayback(ctx)
	return nil
}

// endPlayback clears the current track after the queue ran out or the player stopped.
func (s *Service) endPlayback(ctx context.Context) {
	if s.cur != nil {
		s.published("player.track_ended", s.cur.trackID)
	}
	s.cur, s.pre, s.paused, s.positionM = nil, nil, false, 0
	s.save(ctx)
	s.publishState(ctx)
}

// refreshPreload keeps the engine's "next file" equal to what the queue would play next.
func (s *Service) refreshPreload(ctx context.Context) {
	if s.cur == nil {
		return
	}
	item, ok, err := s.Queue.Peek(ctx)
	if err != nil {
		s.Log.Warn("playback: peek", slog.Any("err", err))
		return
	}
	qs, _ := s.Queue.State(ctx)
	want := ok && qs.Repeat != "one" && item.Position != s.cur.pos
	switch {
	case !want && s.pre != nil:
		if err := s.Engine.ClearPreload(ctx); err != nil {
			s.Log.Warn("playback: clear preload", slog.Any("err", err))
		}
		s.pre = nil
	case want && (s.pre == nil || s.pre.pos != item.Position):
		if s.pre != nil {
			_ = s.Engine.ClearPreload(ctx)
			s.pre = nil
		}
		path, err := s.Tracks.TrackPath(ctx, item.TrackID)
		if err == nil {
			err = s.Engine.Preload(ctx, path)
		}
		if err != nil {
			s.Log.Warn("playback: preload", slog.String("track_id", item.TrackID), slog.Any("err", err))
			return
		}
		s.pre = &preloaded{trackID: item.TrackID, pos: item.Position}
	}
}

// handle reacts to one engine event.
func (s *Service) handle(ctx context.Context, ev EngineEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev.Kind {
	case EvPosition:
		s.onPosition(ctx, ev.PositionMS)
	case EvEnded:
		if ev.Reason == ReasonEOF || ev.Reason == ReasonError {
			s.onEnded(ctx, ev.Reason)
		}
	case EvRestarted:
		s.reload(ctx)
	}
}

func (s *Service) onPosition(ctx context.Context, ms int64) {
	if s.cur == nil {
		return
	}
	if d := ms - s.cur.lastPosMS; d > 0 && d <= 2000 && !s.paused {
		s.cur.listenedMS += d // only continuous playback counts, not seeks
	}
	s.cur.lastPosMS, s.positionM = ms, ms
	if now := s.Now(); !s.paused && now.Sub(s.lastEmit) >= time.Second {
		s.lastEmit = now
		s.Bus.Publish(events.Event{Type: "player.position", Data: map[string]int64{"position_ms": ms, "duration_ms": s.cur.durationMS}})
	}
}

// onEnded advances the queue when a file ended by itself (or failed to play).
func (s *Service) onEnded(ctx context.Context, reason string) {
	if s.cur == nil {
		return
	}
	if reason == ReasonError {
		s.failures++
		s.Log.Warn("playback: track failed to play", slog.String("track_id", s.cur.trackID))
	} else {
		s.failures = 0
	}
	s.finishCurrent(ctx, reason == ReasonEOF)
	s.published("player.track_ended", s.cur.trackID)
	s.cur = nil

	gapless := reason == ReasonEOF
	for ; s.failures < maxLoadFailures; s.failures++ {
		item, ok, err := s.Queue.Next(ctx, true)
		if err != nil {
			s.Log.Error("playback: advance queue", slog.Any("err", err))
			return
		}
		if !ok {
			s.endPlayback(ctx)
			return
		}
		s.topUpRadio(ctx, item)

		if gapless && s.pre != nil && s.pre.pos == item.Position {
			// the engine already moved on to the preloaded file: just follow it
			if tr, err := s.Tracks.Track(ctx, item.TrackID); err == nil {
				s.pre = nil
				s.setCurrent(item, tr)
				s.cur.lastPosMS = 0
				s.published("player.track_started", item.TrackID)
				s.publishState(ctx)
				s.refreshPreload(ctx)
				return
			}
		}
		gapless = false
		if err := s.load(ctx, item, 0, false); err != nil {
			s.Log.Error("playback: cannot play track, skipping", slog.String("track_id", item.TrackID), slog.Any("err", err))
			continue // counts as one more failure
		}
		s.failures = 0
		s.published("player.track_started", item.TrackID)
		s.publishState(ctx)
		return
	}
	s.Log.Error("playback: giving up after repeated failures")
	_ = s.Engine.Stop(ctx)
	s.endPlayback(ctx)
}

// topUpRadio keeps a radio queue from running dry.
func (s *Service) topUpRadio(ctx context.Context, item queue.Item) {
	if s.Radio == nil || item.Source != "radio" {
		return
	}
	left, err := s.Queue.Remaining(ctx)
	if err != nil || left >= radioLowWater {
		return
	}
	ids, err := s.Radio.More(ctx, item.SourceID, radioBatch)
	if err != nil || len(ids) == 0 {
		if err != nil {
			s.Log.Warn("playback: radio refill", slog.Any("err", err))
		}
		return
	}
	if err := s.Queue.Extend(ctx, ids, "radio", item.SourceID); err != nil {
		s.Log.Warn("playback: radio extend", slog.Any("err", err))
	}
}

// ---- persistence and events ----

func (s *Service) save(ctx context.Context) {
	track := sql.NullString{}
	if s.cur != nil {
		track = sql.NullString{String: s.cur.trackID, Valid: true}
	}
	err := db.New(s.Store.W).SavePlayerState(ctx, db.SavePlayerStateParams{
		TrackID: track, PositionMs: s.positionM, Volume: int64(s.volume),
		Output: sql.NullString{String: s.Output, Valid: s.Output != ""}, UpdatedAt: store.Now(),
	})
	if err != nil {
		s.Log.Error("playback: save state", slog.Any("err", err))
	}
}

func (s *Service) published(typ, trackID string) {
	s.Bus.Publish(events.Event{Type: typ, Data: map[string]string{"track_id": trackID}})
}

func (s *Service) publishState(ctx context.Context) {
	qs, _ := s.Queue.State(ctx)
	st := State{Volume: s.volume, Shuffle: qs.Shuffle, Repeat: qs.Repeat, PositionMS: s.positionM}
	if s.cur != nil {
		st.TrackID, st.DurationMS, st.Playing = s.cur.trackID, s.cur.durationMS, !s.paused
	}
	s.Bus.Publish(events.Event{Type: "player.state_changed", Data: st})
}

// Resolve expands a context spec ("album:ID") into tracks; the API uses it to queue whole albums.
func (s *Service) Resolve(ctx context.Context, spec string) (PlayContext, error) {
	return s.Contexts.Resolve(ctx, spec)
}
