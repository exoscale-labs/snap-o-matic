// +build !windows,!plan9

package log

import (
	log "gopkg.in/inconshreveable/log15.v2"
)

// Returns the log handler based on the log configuration string. This function is platform-dependent.
func GetLogHandler(logTo string) log.Handler {
	var logHandler log.Handler
	var err error
	switch logTo {
	case "-", "":
		logHandler = log.StdoutHandler

	case ":syslog":
		if logHandler, err = log.SyslogHandler(
			syslog.LOG_INFO|syslog.LOG_LOCAL0,
			"snap-o-matic",
			log.LogfmtFormat(),
		); err != nil {
			log.Error("unable to initialize syslog logging", err)
		}

	default:
		if logHandler, err = log.FileHandler(logTo, log.LogfmtFormat()); err != nil {
			log.Error("unable to initialize file logging", err)
		}
	}
	return logHandler
}
