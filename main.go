package main

import (
	"flag"
	"log"
	"net"
	"os"
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
)

func main() {
	flag.Parse()

	var sName string
	if *serverName == "" {
		sName = "localhost"
	} else {
		sName = *serverName
	}
	localAddr := net.TCPAddr{Port: *port}
	ip := net.ParseIP(*host)
	if ip == nil {
		log.Fatal("Invalid IP address to listen on")
	}
	localAddr.IP = ip
	log.Printf("Starting server on %v", localAddr.String())
	if *fileDownloadLocation != "" {
		dInfo, err := os.Stat(*fileDownloadLocation)
		if err != nil || !dInfo.IsDir() {
			log.Fatalf("Missing or invalid download directory: %s", *fileDownloadLocation)
		}
	}
	server := Server{
		LocalAddr:            &localAddr,
		Name:                 sName,
		ChunkSize:            *chunkSize,
		FileDownloadLocation: *fileDownloadLocation,
		FileProxyPrefix:      *fileProxyPrefix,
	}
	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
}
