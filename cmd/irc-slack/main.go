package main

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net"
	"os"

	"github.com/insomniacslk/irc-slack/pkg/ircslack"

	"github.com/coredhcp/coredhcp/logger"
	"github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
)

// Version information. Will be populated with the git revision and branch
// information when running `make`.
var (
	ProgramName        = "irc-slack"
	Version     string = "unknown (please build with `make`)"
)

// To authenticate, the IRC client has to send a PASS command with a Slack
// legacy token for the desired team. See README.md for details.
var (
	port                 = flag.IntP("port", "p", 6666, "Local port to listen on")
	host                 = flag.StringP("host", "H", "127.0.0.1", "IP address to listen on")
	serverName           = flag.StringP("server", "s", "", "IRC server name (i.e. the host name to send to clients)")
	chunkSize            = flag.IntP("chunk", "C", 512, "Maximum size of a line to send to the client. Only works for certain reply types")
	fileDownloadLocation = flag.StringP("download", "d", "", "If set will download attachments to this location")
	fileProxyPrefix      = flag.StringP("fileprefix", "l", "", "If set will overwrite urls to attachments with this prefix and local file name inside the path set with -d")
	logLevel             = flag.StringP("loglevel", "L", "info", fmt.Sprintf("Log level. One of %v", getLogLevels()))
	flagSlackDebug       = flag.BoolP("debug", "D", false, "Enable debug logging of the Slack API")
	flagPagination       = flag.IntP("pagination", "P", 0, "Pagination value for API calls. If 0 or unspecified, use the recommended default (currently 200). Larger values can help on large Slack teams")
	flagKey              = flag.StringP("key", "k", "", "TLS key for HTTPS server. Requires -cert")
	flagCert             = flag.StringP("cert", "c", "", "TLS certificate for HTTPS server. Requires -key")
	flagVersion          = flag.BoolP("version", "v", false, "Print version and exit")
)

var log = logger.GetLogger("main")

var logLevels = map[string]func(*logrus.Logger){
	"none":    func(l *logrus.Logger) { l.SetOutput(ioutil.Discard) },
	"debug":   func(l *logrus.Logger) { l.SetLevel(logrus.DebugLevel) },
	"info":    func(l *logrus.Logger) { l.SetLevel(logrus.InfoLevel) },
	"warning": func(l *logrus.Logger) { l.SetLevel(logrus.WarnLevel) },
	"error":   func(l *logrus.Logger) { l.SetLevel(logrus.ErrorLevel) },
	"fatal":   func(l *logrus.Logger) { l.SetLevel(logrus.FatalLevel) },
}

func getLogLevels() []string {
	var levels []string
	for k := range logLevels {
		levels = append(levels, k)
	}
	return levels
}

func main() {
	flag.Parse()
	if *flagVersion {
		fmt.Printf("%s version %s\n", ProgramName, Version)
		os.Exit(0)
	}

	fn, ok := logLevels[*logLevel]
	if !ok {
		log.Fatalf("Invalid log level '%s'. Valid log levels are %v", *logLevel, getLogLevels())
	}
	fn(log.Logger)
	log.Infof("Setting log level to '%s'", *logLevel)
	var sName string
	if *serverName == "" {
		sName = "localhost"
	} else {
		sName = *serverName
	}
	localAddr := net.TCPAddr{Port: *port}
	ip := net.ParseIP(*host)
	if ip == nil {
		log.Fatalf("Invalid IP address to listen on: '%s'", *host)
	}
	localAddr.IP = ip
	log.Printf("Starting server on %v", localAddr.String())
	if *fileDownloadLocation != "" {
		dInfo, err := os.Stat(*fileDownloadLocation)
		if err != nil || !dInfo.IsDir() {
			log.Fatalf("Missing or invalid download directory: %s", *fileDownloadLocation)
		}
	}
	doTLS := false
	if *flagKey != "" && *flagCert != "" {
		doTLS = true
	}
	var tlsConfig *tls.Config
	if doTLS {
		if *flagKey == "" || *flagCert == "" {
			log.Fatalf("-key and -cert must be specified together")
		}
		cert, err := tls.LoadX509KeyPair(*flagCert, *flagKey)
		if err != nil {
			log.Fatalf("Failed to load TLS key/cert: %v", err)
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	}
	server := ircslack.Server{
		LocalAddr:            &localAddr,
		Name:                 sName,
		ChunkSize:            *chunkSize,
		FileDownloadLocation: *fileDownloadLocation,
		FileProxyPrefix:      *fileProxyPrefix,
		SlackDebug:           *flagSlackDebug,
		Pagination:           *flagPagination,
		TLSConfig:            tlsConfig,
	}
	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
}
