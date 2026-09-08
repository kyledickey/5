package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/quackdiscord/bot/internal/v4import"
)

// exportLegacy uses a separate explicit source DSN and never opens the target
// database or reads .env. A read-only source account is recommended in addition
// to the exporter's read-only transaction. Existing exports are never replaced.
func exportLegacy(ctx context.Context, args []string, output io.Writer) error {
	set := flag.NewFlagSet("export", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	sourceGuild := set.String("legacy-guild", "", "legacy Discord guild ID")
	targetGuild := set.String("guild", "", "target v5 guild ULID")
	file := set.String("file", "", "new private JSONL output file")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *sourceGuild == "" || *targetGuild == "" || *file == "" {
		return errors.New("--legacy-guild, --guild, and --file are required")
	}
	dsn := os.Getenv("V4_DATABASE_DSN")
	if dsn == "" {
		return errors.New("V4_DATABASE_DSN is required for the read-only legacy source")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		return errors.New("V4_DATABASE_DSN must be a valid MySQL DSN")
	}
	config.ParseTime, config.Loc = true, time.UTC
	if config.Params == nil {
		config.Params = make(map[string]string)
	}
	// MySQL TIMESTAMP conversion follows the session zone, independently of Loc.
	config.Params["time_zone"] = "'+00:00'"
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	var buffer bytes.Buffer
	count, err := v4import.Export(ctx, db, *sourceGuild, *targetGuild, &buffer)
	if err != nil {
		return err
	}
	destination, err := os.OpenFile(*file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := buffer.WriteTo(destination)
	closeErr := destination.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(*file)
		return errors.Join(writeErr, closeErr)
	}
	_, err = fmt.Fprintf(output, "Exported %d historical cases. Source database unchanged.\n", count)
	return err
}
