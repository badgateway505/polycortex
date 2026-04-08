package web

import (
	"encoding/json"
	"net/http"

	"github.com/badgateway/poly/internal/polymarket"
)

// commentNode is a comment with its replies nested inside, for tree rendering.
type commentNode struct {
	polymarket.Comment
	Replies []*commentNode `json:"replies,omitempty"`
}

// POST /api/comments/{id} — fetch and return threaded comments for a market's event.
// The market must be in the last scan (we need its EventID and token IDs).
func (s *Server) handleComments(w http.ResponseWriter, r *http.Request) {
	marketID := r.PathValue("id")
	sig, ok := s.session.GetSignalByMarketID(marketID)
	if !ok {
		http.Error(w, "market not found in last scan", http.StatusNotFound)
		return
	}

	if sig.Market.EventID == 0 {
		http.Error(w, "market has no event ID — comments unavailable", http.StatusUnprocessableEntity)
		return
	}

	// Parse YES/NO token IDs from ClobTokenIds JSON string: ["yes_id","no_id"]
	var tokenIDs []string
	if sig.Market.ClobTokenIds != "" {
		_ = json.Unmarshal([]byte(sig.Market.ClobTokenIds), &tokenIDs)
	}
	var yesTokenID, noTokenID string
	if len(tokenIDs) >= 2 {
		yesTokenID = tokenIDs[0]
		noTokenID = tokenIDs[1]
	}

	gamma := polymarket.NewGammaClient(s.logger)
	comments, err := gamma.GetEventComments(sig.Market.EventID, yesTokenID, noTokenID)
	if err != nil {
		s.logger.Warn("failed to fetch comments", "market_id", marketID, "event_id", sig.Market.EventID, "err", err)
		http.Error(w, "failed to fetch comments: "+err.Error(), http.StatusBadGateway)
		return
	}

	writeJSON(w, map[string]any{
		"total":    len(comments),
		"tree":     buildCommentTree(comments),
		"flat":     comments,
	})
}

// buildCommentTree takes a flat list of comments and returns top-level nodes
// with replies nested inside, preserving chronological order at each level.
func buildCommentTree(comments []polymarket.Comment) []*commentNode {
	index := make(map[string]*commentNode, len(comments))
	for i := range comments {
		index[comments[i].ID] = &commentNode{Comment: comments[i]}
	}

	var roots []*commentNode
	for i := range comments {
		node := index[comments[i].ID]
		if comments[i].ParentCommentID == "" {
			roots = append(roots, node)
		} else if parent, ok := index[comments[i].ParentCommentID]; ok {
			parent.Replies = append(parent.Replies, node)
		} else {
			// Parent not in this batch — treat as root
			roots = append(roots, node)
		}
	}
	return roots
}
