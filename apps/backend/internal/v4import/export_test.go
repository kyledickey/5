package v4import

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TestExportPagesPreserveEverySourceIdentity covers timestamp ties and exact
// page boundaries without requiring a live source or target database.
func TestExportPagesPreserveEverySourceIdentity(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE cases (id TEXT PRIMARY KEY, guild_id TEXT, user_id TEXT, moderator_id TEXT, reason TEXT, type INTEGER, created_at DATETIME, context_url TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e", "b", "a", "d", "c"} {
		_, err = db.Exec(`INSERT INTO cases VALUES (?, '3001', '4001', '5001', 'reason', 0, ?, NULL)`, id, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
	}
	var all bytes.Buffer
	if _, err := Export(context.Background(), db, "3001", "01J40000000000000000000001", &all); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 2, 5, 6} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var combined bytes.Buffer
			var offset int64
			for {
				var page bytes.Buffer
				result, err := ExportPage(context.Background(), db, "3001", "01J40000000000000000000001", &page, ExportOptions{Limit: limit, Offset: offset})
				if err != nil {
					t.Fatal(err)
				}
				if result.Count > limit || result.Count == 0 {
					t.Fatalf("page: %+v", result)
				}
				combined.Write(page.Bytes())
				if !result.HasMore {
					if result.NextOffset != 0 {
						t.Fatal(result)
					}
					break
				}
				if result.NextOffset != offset+int64(result.Count) {
					t.Fatal(result)
				}
				offset = result.NextOffset
			}
			if combined.String() != all.String() {
				t.Fatal("paging changed source identities or row content")
			}
			var ids []string
			decoder := json.NewDecoder(&combined)
			for decoder.More() {
				var row LegacyCase
				if err := decoder.Decode(&row); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, row.SourceID)
			}
			if !reflect.DeepEqual(ids, []string{"a", "b", "c", "d", "e"}) {
				t.Fatal(ids)
			}
		})
	}
	var empty bytes.Buffer
	result, err := ExportPage(context.Background(), db, "3001", "01J40000000000000000000001", &empty, ExportOptions{Limit: 2, Offset: 5})
	if err != nil || result.Count != 0 || result.HasMore || empty.Len() != 0 {
		t.Fatalf("exhausted: %+v %v", result, err)
	}
}

// TestExportBoundsFailBeforeSourceAccess uses an unopened source to ensure bounds
// are validated before beginning any transaction or emitting data.
func TestExportBoundsFailBeforeSourceAccess(t *testing.T) {
	for _, options := range []ExportOptions{{Limit: -1}, {Limit: 100001}, {Limit: 2, Offset: -1}, {Offset: 1}, {Limit: 1, Offset: 9223372036854775807}} {
		var output bytes.Buffer
		if _, err := ExportPage(context.Background(), nil, "3001", "target", &output, options); err == nil || err.Error() != options.Validate().Error() || output.Len() != 0 {
			t.Fatalf("%+v: %v", options, err)
		}
	}
}
