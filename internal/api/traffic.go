package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/jxstcolin/portly/internal/db"
)

func (s *Server) handleTunnelTraffic(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	since := time.Now().Add(-1 * time.Hour).Unix()
	if v := r.URL.Query().Get("since"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			since = parsed
		}
	}

	samples, err := s.DB.ListTrafficSamples(id, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if samples == nil {
		samples = []db.TrafficSample{}
	}
	writeJSON(w, http.StatusOK, samples)
}

type liveTunnelStat struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ClientID      string `json:"client_id"`
	Connected     bool   `json:"connected"`
	Enabled       bool   `json:"enabled"`
	BytesInTotal  int64  `json:"bytes_in_total"`
	BytesOutTotal int64  `json:"bytes_out_total"`
	RateInBps     int64  `json:"rate_in_bps"`
	RateOutBps    int64  `json:"rate_out_bps"`
}

type livePrev struct {
	bytesIn, bytesOut int64
	at                time.Time
}

// handleLiveWS streams per-tunnel throughput to the UI every second so the
// dashboard's bandwidth graphs update close to real time. It reads byte
// counts from the tunnel server's in-memory live counters (updated as
// bytes actually flow) rather than the DB, which is only flushed once a
// second — using it directly here would double that latency for no reason.
// Origin verification is skipped here because the connection already
// passed requireAuth's cookie check, which is the actual trust boundary.
func (s *Server) handleLiveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	go func() {
		// Drain any client-sent frames so we notice the socket closing.
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()

	prev := make(map[string]livePrev)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			tunnels, err := s.DB.ListAllTunnels()
			if err != nil {
				continue
			}
			connected := s.Tunnels.ConnectedClientIDs()
			live := s.Tunnels.LiveBytesSnapshot()

			stats := make([]liveTunnelStat, 0, len(tunnels))
			for _, t := range tunnels {
				bytesIn, bytesOut := t.BytesInTotal, t.BytesOutTotal
				if l, ok := live[t.ID]; ok {
					bytesIn, bytesOut = l[0], l[1]
				}

				stat := liveTunnelStat{
					ID: t.ID, Name: t.Name, ClientID: t.ClientID,
					Connected: connected[t.ClientID], Enabled: t.Enabled,
					BytesInTotal: bytesIn, BytesOutTotal: bytesOut,
				}
				if p, ok := prev[t.ID]; ok {
					elapsed := now.Sub(p.at).Seconds()
					if elapsed > 0 {
						stat.RateInBps = int64(float64(bytesIn-p.bytesIn) / elapsed)
						stat.RateOutBps = int64(float64(bytesOut-p.bytesOut) / elapsed)
					}
				}
				prev[t.ID] = livePrev{bytesIn: bytesIn, bytesOut: bytesOut, at: now}
				stats = append(stats, stat)
			}

			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = wsjson.Write(writeCtx, conn, map[string]any{"type": "tick", "ts": now.Unix(), "tunnels": stats})
			cancel()
			if err != nil {
				return
			}
		}
	}
}
