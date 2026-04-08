//go:build ignore

package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/badgateway/poly/internal/polymarket"
)

var geoKeywords = []string{
	"war", "ceasefire", "invasion", "troops", "military", "nato", "missile",
	"nuclear", "sanctions", "weapons", "attack", "occupied", "conflict",
	"russia", "ukraine", "israel", "gaza", "iran", "north korea", "taiwan",
	"china", "syria", "palestine", "hezbollah", "hamas", "putin", "zelensky",
	"treaty", "alliance", "diplomatic", "ambassador", "g7", "g20",
	"united nations", "iaea", "coup", "regime", "tariff",
}

func main() {
	_ = godotenv.Load()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	fmt.Fprintln(os.Stderr, "Fetching up to 5000 markets...")
	client := polymarket.NewGammaClient(logger)
	markets, err := client.FetchMarketsLimit(5000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Fetched %d markets\n\n", len(markets))

	type hit struct {
		question  string
		keyword   string
		liquidity float64
	}
	var hits []hit
	keywordCount := map[string]int{}

	for _, m := range markets {
		lower := strings.ToLower(m.Question)
		for _, kw := range geoKeywords {
			if strings.Contains(lower, kw) {
				hits = append(hits, hit{m.Question, kw, m.LiquidityNum})
				keywordCount[kw]++
				break
			}
		}
	}

	fmt.Printf("=== GEOPOLITICS SCAN ===\n")
	fmt.Printf("Matched: %d / %d markets\n\n", len(hits), len(markets))

	fmt.Printf("--- Keyword breakdown ---\n")
	type kv struct{ k string; v int }
	var sorted []kv
	for k, v := range keywordCount {
		sorted = append(sorted, kv{k, v})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].v > sorted[i].v {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	for _, kv := range sorted {
		if kv.v > 0 {
			fmt.Printf("  %-20s %d\n", kv.k, kv.v)
		}
	}

	fmt.Printf("\n--- Sample markets (first 40) ---\n")
	for i, h := range hits {
		if i >= 40 {
			break
		}
		q := h.question
		if len(q) > 85 {
			q = q[:82] + "..."
		}
		fmt.Printf("  [%-14s $%5.0fK] %s\n", h.keyword, h.liquidity/1000, q)
	}
}
