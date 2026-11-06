package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/go-gost/llm-gw/internal/stats"
)

const (
	defaultWSMaxAge = 7 * 24 * time.Hour
	wsWriteTimeout  = 5 * time.Second
	wsReadTimeout   = 60 * time.Second
	wsPingInterval  = 30 * time.Second
	wsReadLimit     = 4096
)

type eventMessage struct {
	Type    string              `json:"type"`
	Since   string              `json:"since,omitempty"`
	To      string              `json:"to,omitempty"`
	Metrics *stats.GraphMetrics `json:"metrics,omitempty"`
	Error   string              `json:"error,omitempty"`
}

type wsClient struct {
	conn      *websocket.Conn
	tokenHash string
	done      chan struct{}
	once      sync.Once
}

func newWSClient(conn *websocket.Conn, tokenHash string) *wsClient {
	return &wsClient{
		conn:      conn,
		tokenHash: tokenHash,
		done:      make(chan struct{}),
	}
}

func (c *wsClient) close(code int, reason string) {
	c.once.Do(func() {
		_ = c.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason),
			time.Now().Add(time.Second),
		)
		_ = c.conn.Close()
		close(c.done)
	})
}

// handleEvents streams absolute graph snapshots over a WebSocket. The
// subscription is fixed for the life of the connection; a range change or a
// reset reconnects.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin not allowed")
		return
	}
	since, err := s.subscriptionSince(r.URL.Query(), time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tokenHash := hashToken(cookie.Value)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := newWSClient(conn, tokenHash)
	s.registerWS(client)
	defer s.unregisterWS(client)
	defer client.close(websocket.CloseNormalClosure, "")

	ctx := r.Context()
	now := time.Now()
	cfg := s.monitorConfig()
	// GraphAggregate reads MAX(rowid) before its SELECT. A request that
	// completes during that read has rowid <= high and a start time at or
	// after now; a small grace on the upper bound keeps it in the snapshot
	// instead of letting the cursor skip it.
	agg, high, err := s.stats.GraphAggregate(ctx, since, now.Add(time.Second))
	if err != nil {
		client.close(websocket.CloseInternalServerErr, err.Error())
		return
	}
	lastRowID := high
	prev := agg.Metrics(cfg.ResolveEntrypointName, cfg.TopologyIncludes, stats.GraphMetrics{}, true)
	lastPct := time.Now()
	if err := writeEvent(client, eventMessage{
		Type:    "snapshot",
		Since:   since.UTC().Format(time.RFC3339),
		To:      now.UTC().Format(time.RFC3339),
		Metrics: &prev,
	}); err != nil {
		return
	}

	startReadPump(client)

	ticker := time.NewTicker(s.wsInterval)
	defer ticker.Stop()
	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-client.done:
			return
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)); err != nil {
				return
			}
		case <-ticker.C:
			if !s.validSessionHash(client.tokenHash) {
				client.close(4401, "session expired")
				return
			}

			now := time.Now()
			if s.wsMaxAge > 0 && now.Sub(since) > s.wsMaxAge {
				client.close(4408, "session too old")
				return
			}
			sinceMs := since.UnixMilli()
			last, err := s.stats.ForEachRequestAfter(ctx, lastRowID, func(row stats.GraphRow) {
				// Requests are attributed by start time. Rows inserted above the
				// cursor are safe to consider; a start time in the future would
				// require a clock jump, and dropping it here would lose it
				// permanently because the cursor advances.
				if row.TS >= sinceMs {
					agg.Add(row)
				}
			})
			if err != nil {
				if writeEvent(client, eventMessage{Type: "error", Error: err.Error()}) != nil {
					return
				}
				continue
			}
			lastRowID = last

			cfg := s.monitorConfig()
			recompute := s.wsPctInterval <= 0 || time.Since(lastPct) >= s.wsPctInterval
			metrics := agg.Metrics(cfg.ResolveEntrypointName, cfg.TopologyIncludes, prev, recompute)
			if recompute {
				lastPct = time.Now()
			}
			prev = metrics
			if err := writeEvent(client, eventMessage{
				Type:    "snapshot",
				Since:   since.UTC().Format(time.RFC3339),
				To:      now.UTC().Format(time.RFC3339),
				Metrics: &metrics,
			}); err != nil {
				return
			}
		}
	}
}

func writeEvent(c *wsClient, msg eventMessage) error {
	c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return c.conn.WriteJSON(msg)
}

func startReadPump(c *wsClient) {
	go func() {
		c.conn.SetReadLimit(wsReadLimit)
		_ = c.conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		c.conn.SetPongHandler(func(string) error {
			return c.conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		})
		for {
			if _, _, err := c.conn.ReadMessage(); err != nil {
				c.close(websocket.CloseNormalClosure, "")
				return
			}
		}
	}()
}

func (s *Server) registerWS(c *wsClient) {
	s.wsMu.Lock()
	s.wsConns[c] = struct{}{}
	s.wsMu.Unlock()
}

func (s *Server) unregisterWS(c *wsClient) {
	s.wsMu.Lock()
	delete(s.wsConns, c)
	s.wsMu.Unlock()
}

func (s *Server) closeWS(tokenHash string) {
	s.wsMu.Lock()
	clients := make([]*wsClient, 0)
	for c := range s.wsConns {
		if c.tokenHash == tokenHash {
			clients = append(clients, c)
		}
	}
	s.wsMu.Unlock()
	for _, c := range clients {
		c.close(4401, "session expired")
	}
}

func (s *Server) closeAllWS() {
	s.wsMu.Lock()
	clients := make([]*wsClient, 0, len(s.wsConns))
	for c := range s.wsConns {
		clients = append(clients, c)
	}
	s.wsMu.Unlock()
	for _, c := range clients {
		c.close(websocket.CloseGoingAway, "server shutting down")
	}
}

// Close closes every live WebSocket. It is idempotent.
func (s *Server) Close() {
	s.closeAllWS()
}

func (s *Server) subscriptionSince(q url.Values, now time.Time) (time.Time, error) {
	maxAge := s.wsMaxAge
	if maxAge <= 0 {
		maxAge = defaultWSMaxAge
	}
	return parseSubscriptionMax(q, now, maxAge)
}

func parseSubscription(q url.Values, now time.Time) (time.Time, error) {
	return parseSubscriptionMax(q, now, defaultWSMaxAge)
}

func parseSubscriptionMax(q url.Values, now time.Time, maxAge time.Duration) (time.Time, error) {
	if q.Get("reset") == "1" {
		return now, nil
	}
	if raw := q.Get("since"); raw != "" {
		since, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid since")
		}
		if since.After(now) {
			since = now
		}
		if maxAge > 0 && since.Before(now.Add(-maxAge)) {
			since = now.Add(-maxAge)
		}
		return since, nil
	}

	raw := q.Get("range")
	if raw == "" {
		raw = "1h"
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < time.Minute || d > 24*time.Hour {
		return time.Time{}, fmt.Errorf("invalid range")
	}
	return now.Add(-d), nil
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
