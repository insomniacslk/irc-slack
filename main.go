package main

import (
	"flag"
	"log"
	"net"
)

// TODO handle expired Slack RTM sessions (e.g. after standby/resume)
// TODO better handling of QUIT
// TODO set TOPIC - SetChannelTopic
// TODO handle channel/user MODE. Set it to +s on Slack groups (private channels)
// TODO handle /me with the me_message subtype
// TODO handle INVITE - InviteUserToChannel
// TODO handle KICK - KickUserFromChannel
// TODO handle return value from IrcSendNumeric

// To authenticate, the IRC client has to send a PASS command with a Slack
// legacy token for the desired team. See
// https://api.slack.com/custom-integrations/legacy-tokens
var (
	port       = flag.Int("p", 6666, "Local port to listen on")
	serverName = flag.String("s", "", "IRC server name (i.e. the host name to send to clients)")
)

func main() {
	flag.Parse()

	var sName string
	if *serverName == "" {
		sName = "localhost"
	} else {
		sName = *serverName
	}
	server := Server{
		LocalAddr: &net.TCPAddr{IP: net.IPv4zero, Port: *port},
		Name:      sName,
	}
	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
}
