// +build windows

package log

import (
	log "gopkg.in/inconshreveable/log15.v2"
	"os"
)

func GetLogHandler(logTo string) log.Handler {
	var logHandler log.Handler
	var err error
	switch logTo {
	case "-", "":
		logHandler = log.StdoutHandler
	case ":syslog":
		log.Error("syslog is not supported on Windows")
		os.Exit(1)

	default:
		if logHandler, err = log.FileHandler(logTo, log.LogfmtFormat()); err != nil {
			log.Error("unable to initialize file logging", err)
			os.Exit(1)
		}
	}

	return logHandler
}
