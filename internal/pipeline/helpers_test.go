package pipeline_test

import (
	"database/sql"
	"testing"

	_ "github.com/margarit/printpipe/internal/printers/fake"
	"github.com/margarit/printpipe/internal/store/storetest"
)

func storeOf(t *testing.T) *sql.DB { return storetest.Open(t) }
