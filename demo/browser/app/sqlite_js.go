//go:build js && wasm

package browser

import (
	"database/sql"

	"github.com/ncruces/go-sqlite3/driver"
)

func init() {
	sql.Register("sqlite", &driver.SQLite{})
}
