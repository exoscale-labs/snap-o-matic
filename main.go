package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	flag "github.com/spf13/pflag"

	v3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/egoscale/v3/credentials"
	exometa "github.com/exoscale/egoscale/v3/metadata"
)

const (
	defaultSnapshotsRetention = 7
	defaultEndpoint           = v3.CHGva2
)

type config struct {
	APIEndpoint        v3.Endpoint
	SnapshotsRetention int
	DryRun             bool
	InstanceID         InstanceID
	CredentialsFile    string
	LogLevel           string
}

func exitWithErr(err error) {
	slog.Error("", "err", err)
	os.Exit(-1)
}

func main() {
	cfg := config{
		APIEndpoint: getAPIEndpoint(),
	}

	parseFlags(&cfg)

	switch cfg.LogLevel {
	case "debug":
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "error":
		slog.SetLogLoggerLevel(slog.LevelError)
	default:
		slog.SetLogLoggerLevel(slog.LevelInfo)
	}

	var creds *credentials.Credentials
	if cfg.CredentialsFile != "" {
		var err error
		creds, err = apiCredentialsFromFile(cfg.CredentialsFile)
		if err != nil {
			exitWithErr(err)
		}
	} else {
		creds = credentials.NewEnvCredentials()
	}

	client, err := v3.NewClient(creds, v3.ClientOptWithEndpoint(cfg.APIEndpoint))
	if err != nil {
		exitWithErr(err)
	}

	ctx := context.Background()

	if err := snap(ctx, client, &cfg); err != nil {
		exitWithErr(err)
	}
}

type InstanceID struct {
	UUID v3.UUID
}

func (instanceID *InstanceID) String() string {
	return instanceID.UUID.String()
}

func (instanceID *InstanceID) Type() string {
	return "v3.UUID"
}

func (instanceID *InstanceID) Set(v string) error {
	parsedID, err := v3.ParseUUID(v)
	if err != nil {
		return fmt.Errorf("failed to parse instance ID: %w", err)
	}

	instanceID.UUID = parsedID

	return nil
}

func parseFlags(cfg *config) {
	flag.StringVarP(&cfg.CredentialsFile, "credentials-file", "f", "",
		"File to read API credentials from")
	flag.VarP(&cfg.InstanceID, "instance-id", "i",
		"ID of the instance to snapshot (disables instance-local lookup)")
	flag.IntVarP(&cfg.SnapshotsRetention, "snapshot-retention", "r", defaultSnapshotsRetention,
		"Maximum snapshots retention")
	flag.StringVarP(&cfg.LogLevel, "log-level", "L", "info", "Logging level, supported values: error,info,debug")
	flag.BoolVarP(&cfg.DryRun, "dry-run", "d", false, "Run in dry-run mode (read-only)")

	flag.ErrHelp = errors.New("") // Don't print "pflag: help requested" when the user invokes the help flags
	flag.Usage = func() {
		_, _ = fmt.Fprintln(os.Stderr, "snap-o-matic - Automatic Exoscale Compute instance volume snapshot")
		_, _ = fmt.Fprintln(os.Stderr, "")
		_, _ = fmt.Fprintln(os.Stderr, "*** WARNING ***")
		_, _ = fmt.Fprintln(os.Stderr, "")
		_, _ = fmt.Fprintln(os.Stderr, "This is experimental software and may not work as intended or may not be continued in the future. Use at your own risk.")
		_, _ = fmt.Fprintln(os.Stderr, "")
		_, _ = fmt.Fprintln(os.Stderr, "Usage:")
		flag.PrintDefaults()
		_, _ = fmt.Fprintf(os.Stderr, `
Supported environment variables:
  EXOSCALE_API_ENDPOINT    Exoscale Compute API endpoint (default %q)
  EXOSCALE_API_KEY         Exoscale API key
  EXOSCALE_API_SECRET      Exoscale API secret

API credentials file format:
  Instead of reading Exoscale API credentials from environment variables, it
  is possible to read those from a file formatted such as:

    api_key=EXOabcdef0123456789abcdef01
    api_secret=AbCdEfGhIjKlMnOpQrStUvWxYz-0123456789aBcDef
`, defaultEndpoint)
	}

	flag.Parse()
}

// rotateSnapshots lists the existing instance snapshots and deletes the oldest ones in order to remain under the
// specified retention threshold.
// WARNING: unlike previous versions of snap-o-matic, this will not preserve user-created snapshots
func rotateSnapshots(ctx context.Context, client *v3.Client, cfg *config) error {
	snapshots, err := client.ListSnapshots(ctx)
	if err != nil {
		return fmt.Errorf("failed to list snapshots: %w", err)
	}

	// sort in ascending order, oldest snapshot first
	slices.SortFunc(snapshots.Snapshots, func(a, b v3.Snapshot) int {
		return -a.CreatedAT.Compare(b.CreatedAT)
	})

	sc := 0
	for _, snapshot := range snapshots.Snapshots {
		if snapshot.Instance.ID != cfg.InstanceID.UUID {
			continue
		}

		slog.Info("found snapshot", "id", snapshot.ID.String(), "created-at", snapshot.CreatedAT)

		if sc++; sc < cfg.SnapshotsRetention {
			continue
		}

		if cfg.DryRun {
			slog.Info("[DRY-RUN] deleting snapshot", "id", snapshot.ID.String())

			continue
		}

		slog.Info("deleting snapshot", "id", snapshot.ID.String(), "created-at", snapshot.CreatedAT)
		op, err := client.DeleteSnapshot(ctx, snapshot.ID)
		if err != nil {
			return fmt.Errorf("failed to delete snapshot: %w", err)
		}

		slog.Info("waiting for operation to succeed", "operation-id", op.ID, "command", op.Reference.Command)
		_, err = client.Wait(ctx, op, v3.OperationStateSuccess)
		if err != nil {
			return fmt.Errorf("delete operation of snapshot did not succeed: %w", err)
		}
	}

	return nil
}

// takeSnapshot takes a new instance snapshot
func takeSnapshot(ctx context.Context, client *v3.Client, cfg *config) error {
	if cfg.DryRun {
		slog.Info("[DRY-RUN] creating snapshot")

		return nil
	}

	slog.Info("creating snapshot")
	op, err := client.CreateSnapshot(ctx, cfg.InstanceID.UUID)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}

	slog.Info("waiting for operation to succeed", "operation-id", op.ID, "command", op.Reference.Command)

	op, err = client.Wait(ctx, op, v3.OperationStateSuccess)
	if err != nil {
		return fmt.Errorf("delete operation of snapshot did not succeed: %w", err)
	}

	slog.Info("snapshot created successfully", "snapshot-id", op.Reference.ID)

	return nil
}

func snap(ctx context.Context, client *v3.Client, cfg *config) error {
	if cfg.InstanceID.UUID == "" {
		slog.Info("looking up instance ID via metadata service")

		resp, err := exometa.Get(ctx, exometa.InstanceID)
		if err != nil {
			return fmt.Errorf("failed to parse instance ID: %w", err)
		}

		instanceID, err := v3.ParseUUID(resp)
		if err != nil {
			return fmt.Errorf("failed to parse instance ID: %w", err)
		}
		cfg.InstanceID.UUID = instanceID

		slog.Info("defaulting to host instance", "instance-id", cfg.InstanceID)
	}

	slog.Info("settings", "config", *cfg)

	if err := rotateSnapshots(ctx, client, cfg); err != nil {
		return err
	}

	return takeSnapshot(ctx, client, cfg)
}

func getAPIEndpoint() v3.Endpoint {
	envApiEndpoint := os.Getenv("EXOSCALE_API_ENDPOINT")
	if envApiEndpoint != "" {
		return v3.Endpoint(envApiEndpoint)
	}

	return defaultEndpoint
}

// apiCredentialsFromFile parses a file containing the API credentials.
func apiCredentialsFromFile(path string) (*credentials.Credentials, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("unable to open credentials file: %w", err)
	}
	defer f.Close()

	apiKey := ""
	apiSecret := ""

	s := bufio.NewScanner(f)
	lineNr := 0
	for s.Scan() {
		if err := s.Err(); err != nil {
			return nil, fmt.Errorf("unable to parse credentials file: %w", err)
		}
		lineNr++
		line := s.Text()

		parts := strings.Split(line, "=")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid credentials line format on line %d (expected key=value)", lineNr)
		}
		k, v := parts[0], parts[1]

		switch strings.ToLower(k) {
		case "api_key":
			apiKey = v

		case "api_secret":
			apiSecret = v

		default:
			return nil, fmt.Errorf("invalid credentials file key on line %d", lineNr)
		}
	}

	return credentials.NewStaticCredentials(apiKey, apiSecret), nil
}
