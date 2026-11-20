package storage

import (
	"context"
	"database/sql"
	"testing"
)

func TestCASNilBody(t *testing.T) {
	c := NewCAS((*sql.DB)(nil), DefaultCodec())
	sha, err := c.PutBody(context.Background(), nil)
	if err != nil || sha != "" {
		t.Fatalf("empty body: sha=%q err=%v", sha, err)
	}
}
