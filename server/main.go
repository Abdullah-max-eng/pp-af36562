// Package main defines a small HTTP server that accepts a JSON payload containing
// a Cypher query, invokes an external "transformer" binary to convert that Cypher
// to SQL, runs the SQL against MySQL, and returns the tabular results as JSON.
//
// Key pieces:
//   - Configuration (env vars with sensible defaults)
//   - HTTP server using Gin
//   - /healthz endpoint (readiness probe)
//   - /query endpoint: Cypher -> SQL -> MySQL -> JSON rows
//   - Structured logging + nice console previews of results
//   - Graceful shutdown on SIGINT/SIGTERM
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

// Config collects all tunable settings for the service. We load these from
// environment variables (optionally via a .env file for local dev).
type Config struct {
	// Address the HTTP server will bind/listen on.
	// Example: ":8081" for all interfaces on port 8081.
	ListenAddr string

	// MySQL connection string (Data Source Name). Typical format:
	//   user:pass@tcp(127.0.0.1:3306)/graph?parseTime=true&loc=Local
	// The exact format depends on the MySQL driver used by your OpenDB function.
	MySQLDSN string

	// Absolute or relative path to the transformer binary that turns Cypher into SQL.
	// Example:
	//   "../transformer-rs/target/release/transformer-rs"
	TransformerPath string
}

// mustLoadConfig loads .env (if present) and reads env vars, falling back to
// safe defaults. It never returns an error (hence "must*"); we rely on sane
// defaults so the app can start even if no env is set.
func mustLoadConfig() Config {
	// Load variables from a .env file if it exists. Missing file is not fatal.
	_ = godotenv.Load()

	return Config{
		// If LISTEN_ADDR is unset, default to :8081
		ListenAddr: getenv("LISTEN_ADDR", ":8081"),

		// If MYSQL_DSN is unset, default to DSNExample() (your helper elsewhere).
		MySQLDSN: getenv("MYSQL_DSN", DSNExample()),

		// If TRANSFORMER_PATH is unset, default to "./transformer-rs"
		TransformerPath: getenv("TRANSFORMER_PATH", "./transformer-rs"),
	}
}

// getenv fetches an env var k; if missing/empty, returns def.
// This pattern centralizes "env var with default" logic.
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// QueryRequest is the JSON we accept at POST /query
//
//	{
//	  "cypher": "MATCH ... RETURN ..."
//	}
type QueryRequest struct {
	Cypher string `json:"cypher"`
}

// QueryResponse is the JSON we return from POST /query.
// - Columns: names of the result columns (order matters).
// - Rows:    data rows (each row is an array of values matching Columns).
// - Error:   non-empty if something went wrong (HTTP status will also reflect this).
// - Raw:     optional arbitrary debug payload (not used here but left for expansion).
type QueryResponse struct {
	Columns []string        `json:"columns,omitempty"`
	Rows    [][]any         `json:"rows,omitempty"`
	Error   string          `json:"error,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

func main() {
	// 1) Load configuration.
	cfg := mustLoadConfig()

	// 2) Open a connection handle to MySQL.
	//    OpenDB is your project helper (not shown in this file). It should:
	//      - connect using cfg.MySQLDSN
	//      - return a handle with a Query(ctx, sql) method that yields (cols, rows, error)
	db, err := OpenDB(cfg.MySQLDSN)
	if err != nil {
		// log.Fatalf terminates the process with a nonzero exit code.
		log.Fatalf("mysql open: %v", err)
	}
	// Ensure we release the DB resources on process exit.
	defer db.Close()

	// 3) Set up the Gin HTTP engine.
	//    - ReleaseMode: no debug noise in logs.
	//    - gin.New():   empty engine (we add middleware manually).
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Add standard middlewares:
	//   - Logger():   per-request access log
	//   - Recovery(): panic-safe guard that returns 500 instead of crashing
	//   - cors():     your CORS middleware (not shown) to allow browser clients
	r.Use(gin.Logger(), gin.Recovery(), cors())

	// Health endpoint for load balancers/Kubernetes.
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// =======================
	//        /query
	// =======================
	// POST /query expects JSON body: {"cypher":"..."}
	// Flow:
	//   a) Validate JSON and non-empty Cypher
	//   b) Log the incoming JSON nicely
	//   c) Transform (Cypher -> SQL) via external binary with a 10s timeout
	//   d) Execute SQL against MySQL
	//   e) Log timing + sample rows
	//   f) Return columns + rows as JSON
	r.POST("/query", func(c *gin.Context) {
		// Parse and validate the incoming request JSON.
		var qr QueryRequest
		if err := c.ShouldBindJSON(&qr); err != nil {
			log.Printf("[/query] bad json: %v", err)
			c.JSON(http.StatusBadRequest, QueryResponse{Error: "bad json"})
			return
		}

		// Trim spaces; reject empty Cypher early.
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

		// Record the total start time for latency breakdowns.
		t0 := time.Now()

		// Create a 10-second context for the transformer stage. If the transformer hangs
		// or is slow, we cancel it and return an informative 502 to the client.
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()

		// TransformCypherToSQL is your project helper (not shown). It should:
		//   - run cfg.TransformerPath as a process (or library)
		//   - pass the Cypher
		//   - collect stdout as SQL text
		//   - respect the provided context for timeouts/cancellation
		sqlText, err := TransformCypherToSQL(ctx, cfg.TransformerPath, cy)
		if err != nil {
			log.Printf("[/query] transformer error: %v", err)
			c.JSON(http.StatusBadGateway, QueryResponse{Error: "transformer error: " + err.Error()})
			return
		}
		// MustNonEmpty is another helper (not shown). We use it to reject empty SQL
		// (e.g., when the transformer doesn't understand the Cypher).
		if _, err := MustNonEmpty(sqlText); err != nil {
			log.Printf("[/query] transformer returned empty SQL")
			c.JSON(http.StatusBadGateway, QueryResponse{Error: "transformer empty SQL"})
			return
		}

		// Log the exact SQL produced, to make debugging easier.
		log.Println("---- Transformer output (SQL) ----")
		log.Println(sqlText)
		log.Println("----------------------------------")

		// Measure the pure SQL execution time (separately from transform time).
		sqlStart := time.Now()

		// Execute the SQL against MySQL. Your db.Query should:
		//   - accept ctx (so the request can be canceled if the client disconnects)
		//   - return []string (column names) and [][]any (row values).
		cols, rows, err := db.Query(c.Request.Context(), sqlText)
		if err != nil {
			// Common errors: syntax error, unknown table/column, etc.
			// We return 400 since the request caused a bad SQL.
			log.Printf("[/query] sql error: %v", err)
			c.JSON(http.StatusBadRequest, QueryResponse{Error: "sql error: " + err.Error()})
			return
		}
		sqlDur := time.Since(sqlStart)

		// Summarize the operation in logs: how many rows, how long each stage took.
		log.Printf("[/query] columns=%v", cols)
		log.Printf("[/query] rows=%d | transform=%s | sql=%s | total=%s",
			len(rows), time.Since(t0)-sqlDur, sqlDur, time.Since(t0))

		// Print up to 10 sample rows in a simple table for humans scanning logs.
		log.Println("---- Sample rows (up to 10) ----")
		printRows(cols, rows, 10)
		log.Println("--------------------------------")

		// Return structured JSON back to the client. We omit Error when there's none.
		c.JSON(http.StatusOK, QueryResponse{Columns: cols, Rows: rows})
	})

	// 4) Spin up the HTTP server with our Gin handler.
	//    We run it in a goroutine so we can concurrently wait for a shutdown signal.
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: r}
	go func() {
		log.Printf("listening on %s", cfg.ListenAddr)
		// ListenAndServe blocks until the server stops or errors.
		// http.ErrServerClosed is the normal "we are shutting down" signal.
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	// 5) Graceful shutdown: wait for OS signals (Ctrl+C or container stop).
	//    When received, we call srv.Shutdown with a timeout context so:
	//      - in-flight requests can finish
	//      - listeners are closed cleanly
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM) // SIGINT + SIGTERM
	<-stop                                             // block here until a signal arrives

	// Give the server up to 5 seconds to drain.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("shutdown complete")
}

// --------- helpers for nicer logs ----------

// toPrettyJSON marshals any Go value as indented JSON for log readability.
// It ignores errors for convenience (worst case: returns "{}").
func toPrettyJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// printRows renders up to `max` rows in a quick "header + dashed line + rows"
// format. This is only for logs (observability), not user-facing output.
func printRows(cols []string, rows [][]any, max int) {
	// If there are no rows, say so and return.
	if len(rows) == 0 {
		log.Println("(no rows)")
		return
	}
	// Clamp max to the number of rows we have.
	if max > len(rows) {
		max = len(rows)
	}

	// Build and print header line: "col1 | col2 | col3"
	header := strings.Join(cols, " | ")
	log.Println(header)
	log.Println(strings.Repeat("-", len(header)))

	// Print each row as pipe-delimited values, converting each cell to string.
	for i := 0; i < max; i++ {
		row := rows[i]
		cells := make([]string, len(row))
		for j, v := range row {
			cells[j] = fmtAny(v)
		}
		log.Println(strings.Join(cells, " | "))
	}

	// If we truncated, mention how many more rows exist.
	if max < len(rows) {
		log.Printf("... %d more rows ...", len(rows)-max)
	}
}

// fmtAny converts any Go value into a printable string for log display.
// Special-cases:
//   - nil  -> "NULL"
//   - string -> unchanged
//   - everything else -> fmt.Sprintf("%v", v)
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
