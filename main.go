package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/caarlos0/env/v6"
	egoScaleLog "github.com/exoscale-labs/snap-o-matic/log"
	"github.com/exoscale/egoscale"
	flag "github.com/spf13/pflag"
	log "gopkg.in/inconshreveable/log15.v2"
)

const (
	metadataURL = "http://169.254.169.254/latest/meta-data/vm-id"
	autosnapTag = "autosnap"

	defaultSnapshotsRetention = 7
)

var (
	exo *egoscale.Client
	cfg config
)

type config struct {
	APIEndpoint        string `env:"EXOSCALE_API_ENDPOINT" envDefault:"https://api.exoscale.com/compute"`
	APIKey             string `env:"EXOSCALE_API_KEY"`
	APISecret          string `env:"EXOSCALE_API_SECRET"`
	SnapshotsRetention int
	DryRun             bool
	InstanceID         string
}

func init() {
	var (
		credsFile  string
		logTo      string
		logLevel   string
		logHandler log.Handler
		err        error
	)

	if err := env.Parse(&cfg); err != nil {
		dieOnError("initialization failed", "error", err)
	}

	flag.StringVarP(&credsFile, "credentials-file", "f", "",
		"File to read API credentials from")
	flag.StringVarP(&cfg.InstanceID, "instance-id", "i", "",
		"ID of the instance to snapshot (disables instance-local lookup)")
	flag.IntVarP(&cfg.SnapshotsRetention, "snapshot-retention", "r", defaultSnapshotsRetention,
		"Maximum snapshots retention")
	flag.StringVarP(&logTo, "log", "l", "-",
		`File to log activity to, "-" to log to stdout or ":syslog" to log to syslog`)
	flag.StringVarP(&logLevel, "log-level", "L", "info", "Logging level, supported values: error,info,debug")
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
  EXOSCALE_API_ENDPOINT    Exoscale Compute API endpoint (default "https://api.exoscale.com/compute")
  EXOSCALE_API_KEY         Exoscale API key
  EXOSCALE_API_SECRET      Exoscale API secret

API credentials file format:
  Instead of reading Exoscale API credentials from environment variables, it
  is possible to read those from a file formatted such as:

    api_key=EXOabcdef0123456789abcdef01
    api_secret=AbCdEfGhIjKlMnOpQrStUvWxYz-0123456789aBcDef
`)
	}

	flag.Parse()

	logLevelHandler, err := log.LvlFromString(logLevel)
	if err != nil {
		dieOnError("invalid value for option --log-level: %s", err)
	}

	logHandler = egoScaleLog.GetLogHandler(logTo)

	log.Root().SetHandler(log.LvlFilterHandler(logLevelHandler, logHandler))

	if credsFile != "" {
		apiCredentialsFromFile(credsFile)
	}
	if cfg.APIKey == "" || cfg.APISecret == "" {
		dieOnError("missing API credentials")
	}

	exo = egoscale.NewClient(cfg.APIEndpoint, cfg.APIKey, cfg.APISecret)
}

func main() {
	var ctx context.Context = context.Background()

	if cfg.InstanceID == "" {
		log.Debug("looking up instance ID via metadata service")

		res, err := http.Get(metadataURL)
		if err != nil {
			dieOnError("unable to find instance ID", "error", err)
		}
		defer res.Body.Close()

		body, err := ioutil.ReadAll(res.Body)
		if err != nil {
			dieOnError("unable to read metadata service request reply", "error", err)
		}
		cfg.InstanceID = string(body)
	}

	log.Debug("settings",
		"api_endpoint", cfg.APIEndpoint,
		"api_key", cfg.APIKey,
		"api_secret", scrambleString(cfg.APISecret),
		"instance_id", cfg.InstanceID,
		"max_snapshots_retention", cfg.SnapshotsRetention,
		"dry-run", cfg.DryRun,
	)

	res, err := exo.GetWithContext(ctx, &egoscale.ListVolumes{
		VirtualMachineID: egoscale.MustParseUUID(cfg.InstanceID),
	})
	if err != nil {
		dieOnError("unable to find instance storage volumes", "error", err)
	}
	instanceVolume := res.(*egoscale.Volume)

	rotateSnapshots(ctx, instanceVolume.ID)
	takeSnapshot(ctx, instanceVolume.ID)
}

// rotateSnapshots lists the existing instance snapshots and deletes the oldest ones in order to remain under the
// specified retention threshold.
func rotateSnapshots(ctx context.Context, instanceVolumeID *egoscale.UUID) {
	res, err := exo.ListWithContext(ctx, &egoscale.ListSnapshots{VolumeID: instanceVolumeID})
	if err != nil {
		dieOnError("unable to list snapshots", "error", err)
	}

	sc := 0
	for _, s := range res {
		snapshot := s.(*egoscale.Snapshot)

		// Our snapshots are tagged with a specific key, so we don't delete the snapshots a user could have taken
		// manually
		if !isAutosnapshot(snapshot) || strings.ToLower(snapshot.State) != "backedup" {
			continue
		}

		log.Debug("found snapshot", "id", snapshot.ID.String())

		if sc++; sc < cfg.SnapshotsRetention {
			continue
		}

		// Snapshots are returned by the API sorted in reverse chronological order, so from the moment we exceed the
		// maximum snapshot retention threshold we can safely delete the remaining snapshots as they are the oldest
		if cfg.DryRun {
			log.Info("[DRY-RUN] deleting snapshot", "id", snapshot.ID.String())
			continue
		}

		log.Info("deleting snapshot", "id", snapshot.ID.String())
		if err := exo.BooleanRequestWithContext(ctx, &egoscale.DeleteSnapshot{ID: snapshot.ID}); err != nil {
			dieOnError("unable to delete snapshot", "error", err)
		}
	}
}

// takeSnapshot takes a new instance snapshot and tags it.
func takeSnapshot(ctx context.Context, instanceVolumeID *egoscale.UUID) {
	if cfg.DryRun {
		log.Info("[DRY-RUN] creating snapshot")
		return
	}

	log.Info("creating snapshot")
	res, err := exo.RequestWithContext(ctx, &egoscale.CreateSnapshot{
		VolumeID: instanceVolumeID,
	})
	if err != nil {
		dieOnError("unable to create new snapshot", "error", err)
	}
	instanceSnapshot := res.(*egoscale.Snapshot)

	if _, err := exo.RequestWithContext(ctx, &egoscale.CreateTags{
		ResourceType: instanceSnapshot.ResourceType(),
		ResourceIDs:  []egoscale.UUID{*(instanceSnapshot.ID)},
		Tags:         []egoscale.ResourceTag{{Key: autosnapTag, Value: "true"}},
	}); err != nil {
		// Cleanup attempt: don't leave our snapshot dangling untagged
		_ = exo.BooleanRequestWithContext(ctx, &egoscale.DeleteSnapshot{ID: instanceSnapshot.ID}) // nolint:errcheck
		dieOnError("unable to tag new snapshot", "error", err)
	}

	log.Debug("snapshot created successfully", "id", instanceSnapshot.ID.String())
}

// isAutosnapshot returns true if the snapshot s is an automatic snapshot.
func isAutosnapshot(s *egoscale.Snapshot) bool {
	for _, tag := range s.Tags {
		if tag.Key == autosnapTag {
			return true
		}
	}

	return false
}

// scrambleString returns a scrambled version of s, with all characters except the first and the last replaced with
// "*".
func scrambleString(s string) string {
	n := len(s)
	switch n {
	case 0:
		return ""

	case 1:
		return "*"

	default:
		scrambled := make([]byte, n)
		for i := range s {
			if i == 0 || i == n-1 {
				scrambled[i] = s[i]
				continue
			}
			scrambled[i] = '*'
		}

		return string(scrambled)
	}
}

// apiCredentialsFromFile parses a file containing the API credentials file and sets the configuration API credentials
// if successful.
func apiCredentialsFromFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		dieOnError("unable to open credentials file", "error", err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		if err := s.Err(); err != nil {
			dieOnError("unable to parse credentials file", "error", err)
		}
		line := s.Text()

		parts := strings.Split(line, "=")
		if len(parts) != 2 {
			dieOnError("invalid credentials line format (expected key=value)", "line", line)
		}
		k, v := parts[0], parts[1]

		switch strings.ToLower(k) {
		case "api_key":
			cfg.APIKey = v

		case "api_secret":
			cfg.APISecret = v

		default:
			dieOnError("invalid credentials file key", "key", k)
		}
	}
}

func dieOnError(msg string, ctx ...interface{}) {
	log.Error(msg, ctx...)
	os.Exit(1)
}
