package sqltables

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
)

var (
	identRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

type Service struct{}

func NewService() *Service { return &Service{} }

type QueryResult struct {
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	RowCount int             `json:"rowCount"`
}

func (s *Service) Query(ctx context.Context, scope *policy.Scope, sqlText string) (QueryResult, error) {
	if scope == nil || scope.Kind() != models.ObjectKindTable {
		return QueryResult{}, policy.ErrKindMismatch
	}
	schema, table, err := s.relation(ctx, scope)
	if err != nil {
		_ = scope.Rollback(ctx)
		return QueryResult{}, err
	}
	if err := ValidateSQL(sqlText, schema, table); err != nil {
		_ = scope.Rollback(ctx)
		return QueryResult{}, err
	}
	if scope.Memory() || scope.Tx() == nil {
		_ = scope.Commit(ctx)
		return QueryResult{Columns: []string{}, Rows: []map[string]any{}, RowCount: 0}, nil
	}
	rows, err := scope.Tx().Query(ctx, sqlText)
	if err != nil {
		_ = scope.Rollback(ctx)
		return QueryResult{}, err
	}
	defer rows.Close()
	fds := rows.FieldDescriptions()
	cols := make([]string, len(fds))
	for i, fd := range fds {
		cols[i] = string(fd.Name)
	}
	out := QueryResult{Columns: cols, Rows: []map[string]any{}}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			_ = scope.Rollback(ctx)
			return QueryResult{}, err
		}
		row := map[string]any{}
		for i, c := range cols {
			row[c] = vals[i]
		}
		out.Rows = append(out.Rows, row)
	}
	out.RowCount = len(out.Rows)
	return out, scope.Commit(ctx)
}

func (s *Service) relation(ctx context.Context, scope *policy.Scope) (schema, table string, err error) {
	if scope.Memory() || scope.Tx() == nil {
		return "syna_t_mem", "data", nil
	}
	err = scope.Tx().QueryRow(ctx, `SELECT schema_name, table_name FROM table_registry WHERE object_id=$1`, scope.ObjectID()).Scan(&schema, &table)
	if err != nil {
		return "", "", errors.New("table object not provisioned")
	}
	if !identRe.MatchString(schema) || !identRe.MatchString(table) {
		return "", "", errors.New("invalid table identifiers")
	}
	return schema, table, nil
}

// ValidateSQL allows a single SELECT/INSERT/UPDATE/DELETE against schema.table only.
func ValidateSQL(sqlText, schema, table string) error {
	sqlText = strings.TrimSpace(sqlText)
	if sqlText == "" {
		return errors.New("empty SQL")
	}
	if strings.Contains(sqlText, ";") {
		return errors.New("multiple statements not allowed")
	}
	lower := strings.ToLower(sqlText)
	forbidden := []string{
		" reset ", " copy ", " grant ", " revoke ", " create ", " drop ", " alter ",
		" truncate ", " vacuum ", " call ", " do ", " listen ", " notify ", " load ",
		" pg_read_file", " dblink", " lo_import", " set role", " set session", " set local",
	}
	padded := " " + lower + " "
	for _, f := range forbidden {
		if strings.Contains(padded, f) {
			return fmt.Errorf("forbidden SQL keyword")
		}
	}
	switch {
	case strings.HasPrefix(lower, "select"), strings.HasPrefix(lower, "insert"), strings.HasPrefix(lower, "update"), strings.HasPrefix(lower, "delete"):
	default:
		return errors.New("only SELECT/INSERT/UPDATE/DELETE allowed")
	}
	qualified := strings.ToLower(schema + "." + table)
	if !strings.Contains(lower, qualified) && !strings.Contains(lower, " "+table+" ") && !strings.Contains(lower, " "+table+"(") &&
		!strings.HasSuffix(lower, " "+table) && !strings.Contains(lower, "into "+table) && !strings.Contains(lower, "from "+table) &&
		!strings.Contains(lower, "update "+table) {
		return fmt.Errorf("SQL must reference only %s", qualified)
	}
	// Reject other schema qualifiers.
	if strings.Contains(lower, ".") {
		re := regexp.MustCompile(`[a-z_][a-z0-9_]*\.[a-z_][a-z0-9_]*`)
		for _, m := range re.FindAllString(lower, -1) {
			if m != qualified {
				return fmt.Errorf("unauthorized relation %s", m)
			}
		}
	}
	return nil
}
