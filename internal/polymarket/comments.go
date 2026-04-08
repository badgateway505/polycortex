package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const commentsEndpoint = "/comments"

// Comment is a single comment from the Gamma comments API.
type Comment struct {
	ID               string          `json:"id"`
	Body             string          `json:"body"`
	ParentEntityType string          `json:"parentEntityType"`
	ParentEntityID   int64           `json:"parentEntityID"`
	ParentCommentID  string          `json:"parentCommentID,omitempty"` // non-empty on replies
	UserAddress      string          `json:"userAddress"`
	ReplyAddress     string          `json:"replyAddress,omitempty"` // wallet being replied to
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
	Profile          CommentProfile  `json:"profile"`
	Reactions        []Reaction      `json:"reactions"`
	ReactionCount    int             `json:"reactionCount"`
	ReportCount      int             `json:"reportCount"`
	Media            []CommentMedia  `json:"media,omitempty"`

	// Computed after fetch: position size in THIS market (0 = no position).
	// Populated by matching Profile.Positions against the market's YES/NO token IDs.
	MarketPositionUSD float64 `json:"market_position_usd,omitempty"`
	PositionSide      string  `json:"position_side,omitempty"` // "YES", "NO", or ""
}

// CommentProfile holds the commenter's public profile and all their positions.
type CommentProfile struct {
	Name                  string             `json:"name"`
	Pseudonym             string             `json:"pseudonym"`
	Bio                   string             `json:"bio"`
	ProxyWallet           string             `json:"proxyWallet"`
	BaseAddress           string             `json:"baseAddress"`
	ProfileImage          string             `json:"profileImage"`
	DisplayUsernamePublic bool               `json:"displayUsernamePublic"`
	Positions             []CommentPosition  `json:"positions"` // ALL positions across all markets
}

// CommentPosition is one token holding in the commenter's portfolio.
type CommentPosition struct {
	TokenID      string `json:"tokenId"`
	PositionSize string `json:"positionSize"` // micro-USDC string (divide by 1e6 for USD)
}

// PositionUSD converts the raw micro-USDC string to a USD float.
func (p CommentPosition) PositionUSD() float64 {
	v, err := strconv.ParseInt(p.PositionSize, 10, 64)
	if err != nil {
		return 0
	}
	return float64(v) / 1e6
}

// Reaction is a user reaction on a comment.
type Reaction struct {
	ID           string `json:"id"`
	CommentID    int64  `json:"commentID"`
	ReactionType string `json:"reactionType"` // e.g. "HEART"
	UserAddress  string `json:"userAddress"`
}

// CommentMedia is an attached GIF or image.
type CommentMedia struct {
	ID             string    `json:"id"`
	Provider       string    `json:"provider"`
	ProviderMediaID string   `json:"providerMediaId"`
	URL            string    `json:"url"`
	MediaType      string    `json:"mediaType"`
	AltText        string    `json:"altText"`
	CreatedAt      time.Time `json:"createdAt"`
}

// GetEventComments fetches all comments for an event, paginating until exhausted.
// eventID is the numeric Gamma event ID (Market.EventID).
// yesTokenID / noTokenID are used to annotate each comment with the commenter's
// position size in this specific market (if any).
func (gc *GammaClient) GetEventComments(eventID int64, yesTokenID, noTokenID string) ([]Comment, error) {
	const pageSize = 100
	var all []Comment

	for offset := 0; ; offset += pageSize {
		url := fmt.Sprintf("%s%s?parent_entity_type=Event&parent_entity_id=%d&get_positions=true&limit=%d&offset=%d",
			gc.baseURL, commentsEndpoint, eventID, pageSize, offset)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("create comments request offset=%d: %w", offset, err)
		}

		resp, err := gc.client.Do(context.Background(), req)
		if err != nil {
			return nil, fmt.Errorf("fetch comments page offset=%d: %w", offset, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("comments API returned %d at offset=%d", resp.StatusCode, offset)
		}

		var page []Comment
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode comments page offset=%d: %w", offset, err)
		}
		resp.Body.Close()

		// Annotate each comment with position in this specific market
		for i := range page {
			annotatePosition(&page[i], yesTokenID, noTokenID)
		}

		all = append(all, page...)

		if len(page) < pageSize {
			break // last page
		}
	}

	return all, nil
}

// annotatePosition sets MarketPositionUSD and PositionSide on a comment by matching
// the commenter's positions against the market's YES and NO token IDs.
func annotatePosition(c *Comment, yesTokenID, noTokenID string) {
	for _, pos := range c.Profile.Positions {
		usd := pos.PositionUSD()
		if usd <= 0 {
			continue
		}
		switch pos.TokenID {
		case yesTokenID:
			c.MarketPositionUSD += usd
			c.PositionSide = "YES"
		case noTokenID:
			c.MarketPositionUSD += usd
			c.PositionSide = "NO"
		}
	}
}
