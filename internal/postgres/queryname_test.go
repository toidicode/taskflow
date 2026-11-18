package postgres

import "testing"

func TestQueryName(t *testing.T) {
	tests := map[string]string{
		"-- name: GetTask :one\nSELECT id FROM tasks WHERE id = $1": "GetTask",
		"-- name: ListTasks :many\nSELECT 1":                        "ListTasks",
		"select version()":                                          "SELECT",
		"/* goose */ INSERT INTO goose_db_version VALUES ($1)":      "INSERT",
	}
	for sql, want := range tests {
		if got := queryName(sql); got != want {
			t.Errorf("queryName(%q) = %q, muốn %q", sql, got, want)
		}
	}
}
