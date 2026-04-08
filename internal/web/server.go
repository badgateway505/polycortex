package web

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/badgateway/poly/internal/analysis"
	"github.com/badgateway/poly/internal/config"
	"github.com/badgateway/poly/internal/exa"
	"github.com/badgateway/poly/internal/grok"
	"github.com/badgateway/poly/internal/monitor"
	"github.com/badgateway/poly/internal/perplexity"
	"github.com/badgateway/poly/internal/polymarket"
)

//go:embed static/*
var staticFS embed.FS

// Server is the web UI HTTP server.
type Server struct {
	cfg               *config.Config
	logger            *slog.Logger
	session           *Session
	tavily            *analysis.TavilyClient
	exa               *exa.Client
	grok              *grok.Client
	perplexity        *perplexity.Client
	queryGenerator    *analysis.QueryGenerator
	conditionParser   *analysis.ConditionParser
	claudeAPIKey      string
	monitor           *monitor.Monitor
	mux               *http.ServeMux
}

// NewServer creates a new web server.
func NewServer(cfg *config.Config, logger *slog.Logger) *Server {
	s := &Server{
		cfg:     cfg,
		logger:  logger,
		session: NewSession(),
		mux:     http.NewServeMux(),
	}

	// Initialize Tavily client if API key is set
	if key := os.Getenv("TAVILY_API_KEY"); key != "" {
		s.tavily = analysis.NewTavilyClient(key, logger)
		logger.Info("Tavily client initialized")
	} else {
		logger.Warn("TAVILY_API_KEY not set — Tavily search unavailable")
	}

	// Initialize Exa client if API key is set
	if key := os.Getenv("EXA_API_KEY"); key != "" {
		s.exa = exa.NewClient(key, logger)
		logger.Info("Exa semantic search client initialized")
	} else {
		logger.Warn("EXA_API_KEY not set — Exa semantic search unavailable")
	}

	// Initialize Grok client if API key is set
	if key := os.Getenv("GROK_API_KEY"); key != "" {
		s.grok = grok.NewClient(key, logger)
		logger.Info("Grok x_search client initialized")
	} else {
		logger.Warn("GROK_API_KEY not set — X/Twitter sentiment search unavailable")
	}

	// Initialize Perplexity Sonar client if API key is set
	if key := os.Getenv("PPLX_API_KEY"); key != "" {
		s.perplexity = perplexity.NewClient(key, logger)
		logger.Info("Perplexity Sonar client initialized")
	} else {
		logger.Warn("PPLX_API_KEY not set — Perplexity research unavailable")
	}

	// Initialize Claude clients if API key is set
	if key := os.Getenv("CLAUDE_API_KEY"); key != "" {
		s.claudeAPIKey = key
		s.conditionParser = analysis.NewConditionParser(key, logger)
		s.queryGenerator = analysis.NewQueryGenerator(key, logger)
		logger.Info("Condition parser and query generator initialized")
	} else {
		logger.Warn("CLAUDE_API_KEY not set — Condition parser and query generator unavailable")
	}

	// Start the market monitor
	clob := polymarket.NewCLOBClient()
	s.monitor = monitor.New(clob, logger)
	s.monitor.Start()

	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	// API endpoints
	s.mux.HandleFunc("POST /api/scan", s.handleScan)
	s.mux.HandleFunc("GET /api/signals", s.handleGetSignals)
	s.mux.HandleFunc("POST /api/tavily/{id}", s.handleTavily)
	s.mux.HandleFunc("POST /api/condition/{id}", s.handleConditionParser)
	s.mux.HandleFunc("GET /api/prompt/perplexity/{id}", s.handlePerplexityPrompt)
	s.mux.HandleFunc("GET /api/prompt/pillarlab/{id}", s.handlePillarlabPrompt)
	s.mux.HandleFunc("GET /api/prompt/auditor/{id}", s.handleAuditorPrompt)
	s.mux.HandleFunc("POST /api/paste/{source}/{id}", s.handlePaste)
	s.mux.HandleFunc("GET /api/paste/{source}/{id}", s.handleGetPaste)

	s.mux.HandleFunc("POST /api/research/{id}", s.handleResearch)
	s.mux.HandleFunc("POST /api/news/{id}", s.handleNews)
	s.mux.HandleFunc("POST /api/exa/{id}", s.handleExa)
	s.mux.HandleFunc("POST /api/grok/{id}", s.handleGrok)
	s.mux.HandleFunc("POST /api/perplexity/{id}", s.handlePerplexityResearch)
	s.mux.HandleFunc("GET /api/prompt/reaudit/{id}", s.handleReauditPrompt)
	s.mux.HandleFunc("POST /api/audit/{id}", s.handleRunAudit)
	s.mux.HandleFunc("POST /api/reaudit/{id}", s.handleRunReaudit)
	s.mux.HandleFunc("GET /api/debug/{id}", s.handleDebugPrompt)
	s.mux.HandleFunc("POST /api/config/pillarlab", s.handleTogglePillarlab)
	s.mux.HandleFunc("POST /api/comments/{id}", s.handleComments)

	// Monitor endpoints
	s.mux.HandleFunc("POST /api/watch/{id}", s.handleWatch)
	s.mux.HandleFunc("DELETE /api/watch/{id}", s.handleUnwatch)
	s.mux.HandleFunc("GET /api/watch", s.handleWatchlist)
	s.mux.HandleFunc("PATCH /api/watch/{id}/rules", s.handleSetRules)
	s.mux.HandleFunc("GET /api/alerts", s.handleAlerts)
	s.mux.HandleFunc("GET /api/alerts/{id}", s.handleAlertsForMarket)
	s.mux.HandleFunc("GET /api/rules", s.handleRules)
	s.mux.HandleFunc("GET /api/watch/{id}/live", s.handleLiveMetrics)

	// Static files
	staticSub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticSub)))

	// Index page — serve index.html for root
	s.mux.HandleFunc("GET /", s.handleIndex)
}

// handleIndex serves the main HTML page.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// Run starts the server on the given address.
func (s *Server) Run(addr string) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 6 * time.Minute, // Opus audit/re-audit can take 2-5min
	}

	fmt.Fprintf(os.Stderr, "Golden Rain running at http://localhost%s\n", addr)
	s.logger.Info("Web server starting", slog.String("addr", addr))
	return srv.ListenAndServe()
}
