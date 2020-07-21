package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"os"

	"github.com/insomniacslk/irc-slack/pkg/ircslack"

	"github.com/coredhcp/coredhcp/logger"
	"github.com/sirupsen/logrus"
)

// TODO handle expired Slack RTM sessions (e.g. after standby/resume)
// TODO better handling of QUIT
// TODO handle channel/user MODE. Set it to +s on Slack groups (private channels)
// TODO handle /me with the me_message subtype
// TODO handle INVITE - InviteUserToChannel
// TODO handle KICK - KickUserFromChannel
// TODO handle return value from IrcSendNumeric

// To authenticate, the IRC client has to send a PASS command with a Slack
// legacy token for the desired team. See
// https://api.slack.com/custom-integrations/legacy-tokens
var (
	port                 = flag.Int("p", 6666, "Local port to listen on")
	host                 = flag.String("H", "127.0.0.1", "IP address to listen on")
	serverName           = flag.String("s", "", "IRC server name (i.e. the host name to send to clients)")
	chunkSize            = flag.Int("chunk", 512, "Maximum size of a line to send to the client. Only works for certain reply types")
	fileDownloadLocation = flag.String("d", "", "If set will download attachments to this location")
	fileProxyPrefix      = flag.String("l", "", "If set will overwrite urls to attachments with this prefix and local file name inside the path set with -d")
	logLevel             = flag.String("L", "info", fmt.Sprintf("Log level. One of %v", getLogLevels()))
	flagSlackDebug       = flag.Bool("D", false, "Enable debug logging of the Slack API")
	flagPagination       = flag.Int("P", 0, "Pagination value for API calls. If 0 or unspecified, use the recommended default (currently 200). Larger values can help on large Slack teams")
	flagKey              = flag.String("key", "", "TLS key for HTTPS server. Requires -cert")
	flagCert             = flag.String("cert", "", "TLS certificate for HTTPS server. Requires -key")
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
