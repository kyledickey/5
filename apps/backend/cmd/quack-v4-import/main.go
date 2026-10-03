package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/v4import"
)

const usage = "usage: quack-v4-import export|import|rollback|check-scope"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	if args[0] == "export" {
		return exportLegacy(ctx, args[1:], output)
	}
	if args[0] == "check-scope" {
		return checkScope(args[1:])
	}
	_ = godotenv.Load(".env")
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		return errors.New("DATABASE_DSN is required")
	}
	db, err := store.OpenMySQL(dsn)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	importer := v4import.New(store.New(db, nil))
	switch args[0] {
	case "import":
		set := flag.NewFlagSet("import", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		file := set.String("file", "", "versioned JSONL source")
		source := set.String("source", "", "stable source name")
		guild := set.String("guild", "", "v5 guild ULID")
		actor := set.String("actor", "", "operator Discord user id")
		dryRun := set.Bool("dry-run", false, "validate and report without writes")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *file == "" {
			return errors.New("--file is required")
		}
		input, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer input.Close()
		report, importErr := importer.Import(ctx, *source, *guild, *actor, input, *dryRun)
		if report != nil {
			if err := json.NewEncoder(output).Encode(report); err != nil {
				return err
			}
		}
		return importErr
	case "rollback":
		set := flag.NewFlagSet("rollback", flag.ContinueOnError)
		set.SetOutput(io.Discard)
		guild := set.String("guild", "", "v5 guild ULID")
		batch := set.String("batch", "", "import batch id")
		actor := set.String("actor", "", "operator Discord user id")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *guild == "" || *batch == "" || *actor == "" {
			return errors.New("--guild, --batch, and --actor are required")
		}
		return importer.Rollback(ctx, *guild, *batch, *actor)
	default:
		return errors.New(usage)
	}
}

func checkScope(args []string) error {
	set := flag.NewFlagSet("check-scope", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	v4 := set.String("v4", "", "comma-separated v4 command names")
	v5 := set.String("v5", "", "comma-separated v5 command names")
	after := set.Bool("after-migration", false, "require removal of direct moderation commands")
	if err := set.Parse(args); err != nil {
		return err
	}
	return v4import.ValidateCommandScopes(split(*v4), split(*v5), *after)
}

func split(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}
