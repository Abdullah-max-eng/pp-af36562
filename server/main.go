package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

type Config struct {
	// Address the HTTP server will bind/listen on.
	// Example: ":8081" for all interfaces on port 8081.
	ListenAddr string

	// MySQL connection string (Data Source Name). Typical format:
	//   user:pass@tcp(127.0.0.1:3306)/graph?parseTime=true&loc=Local
	MySQLDSN string

	//   "../transformer-rs/target/release/transformer-rs"
	TransformerPath string
}

// mustLoadConfig loads .env (if present) and reads env vars, falling back to
// safe defaults. It never returns an error (hence "must*").
func mustLoadConfig() Config {
	// Load variables from a .env file if it exists. Missing file is not fatal.
	_ = godotenv.Load()

	return Config{
		// If LISTEN_ADDR is unset, default to :8081
		ListenAddr: getenv("LISTEN_ADDR", ":8081"),

		// If MYSQL_DSN is unset, default to DSNExample() (your helper in db.go).
		MySQLDSN: getenv("MYSQL_DSN", DSNExample()),

		// If TRANSFORMER_PATH is unset, default to "./transformer-rs"
		TransformerPath: getenv("TRANSFORMER_PATH", "./transformer-rs"),
	}
}

// getenv fetches an env var k; if missing/empty, returns def.
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// HTTP payload types
// QueryRequest is the JSON we accept at POST /query:
//
//	{
//	  "cypher": "MATCH ... RETURN ..."
type QueryRequest struct {
	Cypher string `json:"cypher"`
}

// QueryResponse is the JSON we return from POST /query.
type QueryResponse struct {
	Columns []string        `json:"columns,omitempty"`
	Rows    [][]any         `json:"rows,omitempty"`
	Error   string          `json:"error,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

// ----------------------------------------------------------------------
// Abstraction for the transformer (for testability)
// ----------------------------------------------------------------------

type transformerFunc func(ctx context.Context, binPath, cypher string) (string, error)

// handleQuery returns a Gin handler that implements the full
// Cypher → transformer → SQL → DB → JSON pipeline.
func handleQuery(db *DB, binPath string, tf transformerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		var qr QueryRequest

		// 1) Parse JSON
		if err := c.ShouldBindJSON(&qr); err != nil {
			log.Printf("[/query] bad json: %v", err)
			c.JSON(http.StatusBadRequest, QueryResponse{Error: "bad json"})
			return
		}

		// 2) Validate cypher
		cy := strings.TrimSpace(qr.Cypher)
		if cy == "" {
			log.Printf("[/query] empty cypher")
			c.JSON(http.StatusBadRequest, QueryResponse{Error: "empty cypher"})
			return
		}

		// Log the incoming payload, pretty-printed, to aid debugging.
		log.Println("---- Request (JSON payload) ----")
		log.Printf("%s\n", toPrettyJSON(qr))
		log.Println("--------------------------------")

		// 3) Run transformer with a timeout
		t0 := time.Now()
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()

		sqlText, err := tf(ctx, binPath, cy)
		if err != nil {
			log.Printf("[/query] transformer error: %v", err)
			c.JSON(http.StatusBadGateway, QueryResponse{Error: "transformer error: " + err.Error()})
			return
		}

		// Reject empty SQL from transformer
		if _, err := MustNonEmpty(strings.TrimSpace(sqlText)); err != nil {
			log.Printf("[/query] transformer returned empty SQL")
			c.JSON(http.StatusBadGateway, QueryResponse{Error: "transformer empty SQL"})
			return
		}

		log.Println("---- Transformer output (SQL) ----")
		log.Println(sqlText)
		log.Println("----------------------------------")

		// 4) Execute SQL against MySQL
		sqlStart := time.Now()
		cols, rows, err := db.Query(c.Request.Context(), sqlText)
		if err != nil {
			log.Printf("[/query] sql error: %v", err)
			c.JSON(http.StatusBadRequest, QueryResponse{Error: "sql error: " + err.Error()})
			return
		}
		sqlDur := time.Since(sqlStart)

		// 5) Log summary
		log.Printf("[/query] columns=%v", cols)
		log.Printf("[/query] rows=%d | transform=%s | sql=%s | total=%s",
			len(rows), time.Since(t0)-sqlDur, sqlDur, time.Since(t0))

		log.Println("---- Sample rows (up to 10) ----")
		// printRows(cols, rows, 10) // uncomment if you want row previews
		log.Println("--------------------------------")

		// 6) Return structured JSON back to the client.
		c.JSON(http.StatusOK, QueryResponse{Columns: cols, Rows: rows})
	}
}

// ----------------------------------------------------------------------
// main()
// ----------------------------------------------------------------------

func main() {
	// 1) Load configuration.
	cfg := mustLoadConfig()
	log.Println("---- Using this Transformer Path ---------------", cfg.TransformerPath)

	// 2) Open MySQL connection.
	db, err := OpenDB(cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("mysql open: %v", err)
	}
	defer db.Close()

	// 3) Set up Gin engine.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), cors())

	// Health endpoint for load balancers / Kubernetes.
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// /query route using our testable handler
	r.POST("/query", handleQuery(db, cfg.TransformerPath, TransformCypherToSQL))

	// 4) HTTP server with graceful shutdown.
	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: r,
	}

	// Run server in a goroutine.
	go func() {
		log.Printf("listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	// Wait for interrupt / terminate signal.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	// Graceful shutdown with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("shutdown complete")
}

// ----------------------------------------------------------------------
// Log helpers
// ----------------------------------------------------------------------

// toPrettyJSON marshals any Go value as indented JSON for log readability.
// It ignores errors for convenience (worst case: returns "{}").
func toPrettyJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// printRows renders up to `max` rows in a quick "header + dashed line + rows"
// format. This is only for logs (observability), not user-facing output.
func printRows(cols []string, rows [][]any, max int) {
	if len(rows) == 0 {
		log.Println("(no rows)")
		return
	}
	if max > len(rows) {
		max = len(rows)
	}

	header := strings.Join(cols, " | ")
	log.Println(header)
	log.Println(strings.Repeat("-", len(header)))

	for i := 0; i < max; i++ {
		row := rows[i]
		cells := make([]string, len(row))
		for j, v := range row {
			cells[j] = fmtAny(v)
		}
		log.Println(strings.Join(cells, " | "))
	}

	if max < len(rows) {
		log.Printf("... %d more rows ...", len(rows)-max)
	}
}

// fmtAny converts any Go value into a printable string for log display.
func fmtAny(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}
