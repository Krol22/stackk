package store

import (
	"context"
	"database/sql"
)

var ctxBG = context.Background()

// connQ adapts a dedicated *sql.Conn to Querier so a transaction can use BEGIN IMMEDIATE.
type connQ struct{ c *sql.Conn }

func (q connQ) Exec(s string, a ...any) (sql.Result, error) { return q.c.ExecContext(ctxBG, s, a...) }
func (q connQ) Query(s string, a ...any) (*sql.Rows, error) { return q.c.QueryContext(ctxBG, s, a...) }
func (q connQ) QueryRow(s string, a ...any) *sql.Row        { return q.c.QueryRowContext(ctxBG, s, a...) }
