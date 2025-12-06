package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// Helpers under test: MustNonEmpty, normalizeDBValue

func TestMustNonEmpty(t *testing.T) {
	t.Run("non-empty", func(t *testing.T) {
		out, err := MustNonEmpty("SELECT 1")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if out != "SELECT 1" {
			t.Fatalf("expected 'SELECT 1', got %q", out)
		}
	})

	t.Run("empty", func(t *testing.T) {
		_, err := MustNonEmpty("")
		if err == nil {
			t.Fatalf("expected error for empty sql, got nil")
		}
		if err != ErrNoSQL {
			t.Fatalf("expected ErrNoSQL, got %v", err)
		}
	})
}

func TestNormalizeDBValue(t *testing.T) {
	t.Run("converts []byte to string", func(t *testing.T) {
		b := []byte("hello")
		got := normalizeDBValue(b)
		s, ok := got.(string)
		if !ok {
			t.Fatalf("expected string, got %T (%v)", got, got)
		}
		if s != "hello" {
			t.Fatalf("expected 'hello', got %q", s)
		}
	})

	t.Run("leaves non-byte values unchanged", func(t *testing.T) {
		n := 42
		got := normalizeDBValue(n)
		if got != n {
			t.Fatalf("expected %v, got %v", n, got)
		}
	})
}

// DB.Query tests (using sqlmock)

// newMockDB creates a DB wrapper around a sqlmock database.
func newMockDB(t *testing.T) (*DB, sqlmock.Sqlmock) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return &DB{sql: sqlDB, kind: "mysql"}, mock
}

func TestDBQuery_SimpleRow(t *testing.T) {
	db, mock := newMockDB(t)
	defer db.Close()

	query := "SELECT id, name FROM users"

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, []byte("Alice")).
		AddRow(2, []byte("Bob"))

	mock.ExpectQuery(regexp.QuoteMeta(query)).WillReturnRows(rows)

	cols, out, err := db.Query(context.Background(), query)
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	if len(cols) != 2 || cols[0] != "id" || cols[1] != "name" {
		t.Fatalf("unexpected columns: %#v", cols)
	}

	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}

	// row 0: [1, "Alice"]
	if out[0][0] != int64(1) && out[0][0] != 1 {
		t.Fatalf("expected first row id=1, got %#v", out[0][0])
	}
	if s, ok := out[0][1].(string); !ok || s != "Alice" {
		t.Fatalf("expected first row name='Alice', got %#v", out[0][1])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// handleQuery tests (HTTP layer)

// fakeTransformer lets us control transformer behavior in tests.
type fakeTransformer struct {
	sqlToReturn string
	err         error
	calls       int
}

// matches transformerFunc type
func (ft *fakeTransformer) Transform(ctx context.Context, binPath, cypher string) (string, error) {
	ft.calls++
	return ft.sqlToReturn, ft.err
}

// newTestRouter sets up a minimal Gin router with /query for tests.
func newTestRouter(db *DB, tf transformerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// We can skip logger/recovery to keep test output clean,
	// but it's also fine to include them.
	r.Use(cors())
	r.POST("/query", handleQuery(db, "/fake/bin", tf))
	return r
}

func TestHandleQuery_BadJSON(t *testing.T) {
	db, _ := newMockDB(t)
	defer db.Close()

	ft := &fakeTransformer{}
	r := newTestRouter(db, ft.Transform)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewBufferString(`{invalid json`))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var resp QueryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Error == "" {
		t.Fatalf("expected error message, got empty")
	}
}

func TestHandleQuery_EmptyCypher(t *testing.T) {
	db, _ := newMockDB(t)
	defer db.Close()

	ft := &fakeTransformer{}
	r := newTestRouter(db, ft.Transform)

	body := `{"cypher": "   "}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleQuery_TransformerError(t *testing.T) {
	db, _ := newMockDB(t)
	defer db.Close()

	ft := &fakeTransformer{
		sqlToReturn: "",
		err:         context.DeadlineExceeded,
	}
	r := newTestRouter(db, ft.Transform)

	body := `{"cypher": "MATCH (n) RETURN n"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestHandleQuery_TransformerEmptySQL(t *testing.T) {
	db, _ := newMockDB(t)
	defer db.Close()

	ft := &fakeTransformer{
		sqlToReturn: "",
		err:         nil,
	}
	r := newTestRouter(db, ft.Transform)

	body := `{"cypher": "MATCH (n) RETURN n"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when transformer returns empty SQL, got %d", w.Code)
	}
}

func TestHandleQuery_Success(t *testing.T) {
	// 1) Mock DB
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	db := &DB{sql: sqlDB, kind: "mysql"}
	defer db.Close()

	// 2) Fake transformer returns simple SQL
	ft := &fakeTransformer{
		sqlToReturn: "SELECT 1 AS id, 'Alice' AS name",
		err:         nil,
	}

	// 3) Expect the query + rows
	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, []byte("Alice"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 AS id, 'Alice' AS name")).
		WillReturnRows(rows)

	r := newTestRouter(db, ft.Transform)

	body := `{"cypher": "MATCH (n) RETURN n"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	// Add a timeout on the request context similar to production (optional)
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	var resp QueryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}

	if len(resp.Columns) != 2 || resp.Columns[0] != "id" || resp.Columns[1] != "name" {
		t.Fatalf("unexpected columns: %#v", resp.Columns)
	}
	if len(resp.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(resp.Rows))
	}
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}

	// Verify DB expectations + transformer calls
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet db expectations: %v", err)
	}
	if ft.calls != 1 {
		t.Fatalf("expected transformer to be called once, got %d", ft.calls)
	}
}
