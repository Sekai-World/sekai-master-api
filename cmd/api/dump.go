package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/storage"
)

const dumpUsage = "usage: sekai-master-api dump --region <region> --entity <entity> [--key <id> | --key <field>=<value>,... | --index <field>=<value>,...]"

// dumpRequest is one parsed `dump` invocation.
type dumpRequest struct {
	region string
	entity string
	// id is a plain record ID (--key 1).
	id string
	// compositeKey holds the key fields of an entity keyed by several fields
	// (--key cardId=1,level=2).
	compositeKey map[string]any
	// index names a relation index by its fields joined with ",", and
	// indexValues holds the lookup values in that order (--index eventId=150).
	index       string
	indexValues []any
}

// dumpReader is the part of the master-data store `dump` reads through.
type dumpReader interface {
	GetByID(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error)
	GetByCompositeKeys(ctx context.Context, region string, entity string, keys []map[string]any) ([]map[string]any, error)
	ListAll(ctx context.Context, region string, entity string) ([]map[string]any, error)
	ListByIndex(ctx context.Context, region string, entity string, index string, lookups [][]any) ([][]map[string]any, error)
}

var errDumpNotFound = errors.New("record not found")

// runDumpCommand prints stored master-data records as JSON. Record blocks are
// opaque to SQL (docs/postgres-master-data-store.md, "Inspecting records"), so
// operators read them through the same store code the API uses.
func runDumpCommand(args []string, stdout io.Writer) (err error) {
	request, err := parseDumpArgs(args)
	if err != nil {
		return err
	}

	cfg := config.Load()
	db, err := storage.OpenDB(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("initialize database for dump: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close dump database: %w", closeErr)
		}
	}()

	store := storage.NewPostgresMasterDataStore(db.Pool, cfg.MasterDataFileConcurrency, "")
	return dumpRecords(context.Background(), store, request, stdout)
}

func parseDumpArgs(args []string) (dumpRequest, error) {
	flags := flag.NewFlagSet("dump", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	region := flags.String("region", "", "region, for example jp")
	entity := flags.String("entity", "", "entity, for example cards")
	key := flags.String("key", "", "record ID, or key fields as field=value,...")
	index := flags.String("index", "", "relation index lookup as field=value,...")
	if err := flags.Parse(args); err != nil {
		return dumpRequest{}, fmt.Errorf("%w\n%s", err, dumpUsage)
	}
	if flags.NArg() > 0 {
		return dumpRequest{}, fmt.Errorf("unexpected arguments %q\n%s", flags.Args(), dumpUsage)
	}

	request := dumpRequest{
		region: strings.ToLower(strings.TrimSpace(*region)),
		entity: strings.ToLower(strings.TrimSpace(*entity)),
	}
	if request.region == "" || request.entity == "" {
		return dumpRequest{}, fmt.Errorf("--region and --entity are required\n%s", dumpUsage)
	}
	if strings.TrimSpace(*key) != "" && strings.TrimSpace(*index) != "" {
		return dumpRequest{}, fmt.Errorf("--key and --index are mutually exclusive\n%s", dumpUsage)
	}

	if rawKey := strings.TrimSpace(*key); rawKey != "" {
		if !strings.Contains(rawKey, "=") {
			request.id = rawKey
			return request, nil
		}
		fields, values, err := parseDumpFieldValues(rawKey)
		if err != nil {
			return dumpRequest{}, fmt.Errorf("--key: %w\n%s", err, dumpUsage)
		}
		request.compositeKey = make(map[string]any, len(fields))
		for position, field := range fields {
			request.compositeKey[field] = values[position]
		}
	}
	if rawIndex := strings.TrimSpace(*index); rawIndex != "" {
		fields, values, err := parseDumpFieldValues(rawIndex)
		if err != nil {
			return dumpRequest{}, fmt.Errorf("--index: %w\n%s", err, dumpUsage)
		}
		request.index = strings.Join(fields, ",")
		request.indexValues = values
	}
	return request, nil
}

// parseDumpFieldValues splits "a=1,b=2" into its fields and values, in order.
func parseDumpFieldValues(raw string) ([]string, []any, error) {
	parts := strings.Split(raw, ",")
	fields := make([]string, 0, len(parts))
	values := make([]any, 0, len(parts))
	for _, part := range parts {
		field, value, ok := strings.Cut(part, "=")
		field = strings.TrimSpace(field)
		if !ok || field == "" {
			return nil, nil, fmt.Errorf("%q is not field=value", part)
		}
		fields = append(fields, field)
		values = append(values, strings.TrimSpace(value))
	}
	return fields, values, nil
}

// dumpRecords prints one record for a key, and a JSON array otherwise: every
// record of the entity, or the records an index lookup matches, in stored
// order.
func dumpRecords(ctx context.Context, reader dumpReader, request dumpRequest, stdout io.Writer) error {
	var output any
	switch {
	case request.id != "":
		record, found, err := reader.GetByID(ctx, request.region, request.entity, request.id)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: %s %s key %s", errDumpNotFound, request.region, request.entity, request.id)
		}
		output = record
	case request.compositeKey != nil:
		records, err := reader.GetByCompositeKeys(ctx, request.region, request.entity, []map[string]any{request.compositeKey})
		if err != nil {
			return err
		}
		if len(records) == 0 || records[0] == nil {
			return fmt.Errorf("%w: %s %s key %v", errDumpNotFound, request.region, request.entity, request.compositeKey)
		}
		output = records[0]
	case request.index != "":
		matches, err := reader.ListByIndex(ctx, request.region, request.entity, request.index, [][]any{request.indexValues})
		if err != nil {
			return err
		}
		records := []map[string]any{}
		if len(matches) > 0 && matches[0] != nil {
			records = matches[0]
		}
		output = records
	default:
		records, err := reader.ListAll(ctx, request.region, request.entity)
		if err != nil {
			return err
		}
		if records == nil {
			records = []map[string]any{}
		}
		output = records
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}
