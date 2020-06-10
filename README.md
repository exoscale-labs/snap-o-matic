# snap-o-matic - Automatic Exoscale Compute instance volume snapshot

```
*** WARNING ***

This is experimental software and may not work as intended or may not be continued in the future. Use at your own risk. 
```

snap-o-matic is an automatic snapshot tool for Exoscale Compute instances.

## Installation

You can install it either by downloading the binaries from the
[releases section](https://github.com/exoscale-labs/snap-o-matic/releases) or as a
[Docker image](https://hub.docker.com/repository/docker/exoscale-labs/snap-o-matic).

**You should configure snap-o-matic to run from a cronjob.**  

## Usage in Docker

You can schedule a **cron job** to run the following command:

```
docker run --rm exoscale-labs/snap-o-matic \
    -e EXOSCALE_API_KEY=exoscale-api-key-here \
    -e EXOSCALE_API_SECRET=exoscale-api-secret-here
```

**Warning!** This will create a single snapshot. You **must** configure this command in a cronjob.

## Usage as a binary

When using it outside of a container environment you can run the `snap-o-matic` program with the following parameters:

 - `-f FILENAME` or `--credentials-file FILENAME`: File to read API credentials from
 - `-d` or `--dry-run`: Run in dry-run mode (do not actually make a snapshot)
 - `-i INSTANCE_ID` or `--instance-id INSTANCE_ID`: The instance to back up. If not provided the instance snap-o-matic is running on will be backed up.
 - `-l LOG_FILE` or `--log LOG_FILE`: File to log activity to, "-" to log to stdout or ":syslog" to log to syslog (default `-`)
 - `-L LOG_LEVEL` or `--log-level LOG_LEVEL` Logging level, supported values: `error`,`info`,`debug` (default `info`)
 - `-r NUMBER` or `--snapshot-retention NUMBER`: Maximum snapshots to keep (default 7)

Credentials can be passed either as a credentials file, or as environment variables. Supported environment variables:

 - `EXOSCALE_API_KEY`: Exoscale API key
 - `EXOSCALE_API_SECRET`: Exoscale API secret

API credentials file format:

```
api_key=EXOabcdef0123456789abcdef01
api_secret=AbCdEfGhIjKlMnOpQrStUvWxYz-0123456789aBcDef
```