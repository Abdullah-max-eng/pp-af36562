package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func main() {
	sql := flag.String("sql", "", "SQL string")
	flag.Parse()

	s := strings.TrimSpace(*sql)
	if s == "" {
		fail("empty sql")
	}

	cql, err := toCypher(s)
	if err != nil {
		fail(err.Error())
	}
	// Print with newline so your shell prompt doesn't stick to the output
	fmt.Println(cql)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

/* ----------------------------- SQL → Cypher ----------------------------- */

func toCypher(sql string) (string, error) {
	// normalize whitespace and drop trailing ;
	s := strings.TrimSpace(strings.TrimSuffix(sql, ";"))
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")

	up := strings.ToUpper(s)
	if !strings.HasPrefix(up, "SELECT ") {
		return "", errors.New("only SELECT supported")
	}

	// split SELECT ... FROM ...
	iFrom := strings.Index(up, " FROM ")
	if iFrom < 0 {
		// support SELECT 1
		expr := strings.TrimSpace(s[len("SELECT "):])
		if expr == "" {
			return "", errors.New("invalid SELECT")
		}
		return fmt.Sprintf("RETURN %s AS value", expr), nil
	}

	selectList := strings.TrimSpace(s[len("SELECT "):iFrom])
	rest := strings.TrimSpace(s[iFrom+len(" FROM "):])
	if rest == "" {
		return "", errors.New("missing table")
	}

	label, where, limit := parseFromWhereLimit(rest)
	alias := strings.ToLower(string(label[0]))

	var b strings.Builder
	// MATCH
	fmt.Fprintf(&b, "MATCH (%s:%s)", alias, label)

	// WHERE
	if where != "" {
		wc := normalizeWhere(where, alias)
		if wc != "" {
			fmt.Fprintf(&b, "\nWHERE %s", wc)
		}
	}

	// RETURN
	ret, err := buildReturn(selectList, alias)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "\n%s", ret)

	// LIMIT
	if limit != "" {
		fmt.Fprintf(&b, "\nLIMIT %s", limit)
	}

	return strings.TrimSpace(b.String()), nil
}

/* --------

----------------------- helpers -------------------------------- */

func parseFromWhereLimit(rest string) (label string, where string, limit string) {
	// rest = "<Label> [WHERE ...] [LIMIT N]"
	parts := strings.SplitN(rest, " ", 2)
	label = parts[0]

	trail := ""
	if len(parts) > 1 {
		trail = strings.TrimSpace(parts[1])
	}
	if trail == "" {
		return
	}

	up := strings.ToUpper(trail)

	// Find WHERE / LIMIT even if they appear at the very beginning (no leading space)
	findWhere := func(s string) int {
		if strings.HasPrefix(s, "WHERE ") { // at start
			return 0
		}
		return strings.Index(s, " WHERE ")
	}
	findLimit := func(s string) int {
		if strings.HasPrefix(s, "LIMIT ") { // at start
			return 0
		}
		return strings.Index(s, " LIMIT ")
	}

	iWhere := findWhere(up)
	iLimit := findLimit(up)

	switch {
	case iWhere >= 0 && iLimit >= 0 && iWhere < iLimit:
		where = strings.TrimSpace(trail[iWhere+len("WHERE ") : iLimit])
		limit = strings.TrimSpace(trail[iLimit+len("LIMIT "):])
	case iWhere >= 0:
		where = strings.TrimSpace(trail[iWhere+len("WHERE "):])
	case iLimit >= 0:
		limit = strings.TrimSpace(trail[iLimit+len("LIMIT "):])
	}
	return
}

// Qualify simple predicates like `id = '2'` → `k.id = '2'`.
// Supports AND/OR with comparisons (=, <>, <=, >=, <, >).
func normalizeWhere(where, alias string) string {
	if where == "" {
		return ""
	}
	reOps := regexp.MustCompile(`(?i)\s+(AND|OR)\s+`)
	clauses := reOps.Split(where, -1)
	ops := reOps.FindAllString(where, -1)

	var out []string
	rePred := regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_\.]*)\s*(=|<>|<=|>=|<|>)\s*(.+?)\s*$`)
	for i, c := range clauses {
		c = strings.TrimSpace(c)
		if m := rePred.FindStringSubmatch(c); m != nil {
			left, op, rhs := m[1], m[2], m[3]
			if !strings.Contains(left, ".") {
				left = alias + "." + left
			}
			out = append(out, fmt.Sprintf("%s %s %s", left, op, rhs))
		} else {
			out = append(out, c) // leave complex pieces as-is
		}
		if i < len(ops) {
			out = append(out, strings.TrimSpace(ops[i]))
		}
	}
	return strings.Join(out, " ")
}

func buildReturn(selectList, alias string) (string, error) {
	sl := strings.TrimSpace(selectList)
	if sl == "" {
		return "", errors.New("empty select list")
	}

	// SELECT *
	if sl == "*" {
		// Never return "* AS *" (invalid in Cypher).
		return fmt.Sprintf("RETURN %s{.*} AS %s", alias, alias), nil
	}

	// SELECT col1, col2, alias.prop, COUNT(*), ...
	items := strings.Split(sl, ",")
	var outs []string
	for _, raw := range items {
		p := strings.TrimSpace(raw)

		// very naive function detection; pass-through with function name alias
		if i := strings.Index(p, "("); i > 0 && strings.HasSuffix(p, ")") {
			fn := strings.ToLower(strings.TrimSpace(p[:i]))
			outs = append(outs, fmt.Sprintf("%s AS %s", p, fn))
			continue
		}

		// qualify bare props
		propExpr := p
		if !strings.Contains(p, ".") {
			propExpr = alias + "." + p
		}
		aliasName := p
		if i := strings.LastIndex(p, "."); i >= 0 && i < len(p)-1 {
			aliasName = p[i+1:]
		}
		outs = append(outs, fmt.Sprintf("%s AS %s", propExpr, aliasName))
	}
	return "RETURN " + strings.Join(outs, ", "), nil
}
