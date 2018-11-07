package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
)

// Server is the server object that exposes the Slack API with an IRC interface.
type Server struct {
	Name        string
	LocalAddr   net.Addr
	Listener    *net.TCPListener
	SlackAPIKey string
	ChunkSize   int
}

// Start runs the IRC server
func (s Server) Start() error {
	listener, err := net.Listen("tcp", s.LocalAddr.String())
	if err != nil {
		return err
	}
	s.Listener = listener.(*net.TCPListener)
	defer s.Listener.Close()
	log.Printf("Listening on %v", s.LocalAddr)
	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			return fmt.Errorf("Error accepting: %v", err)
		}
		go s.HandleRequest(conn.(*net.TCPConn))
	}
}

// HandleRequest handle IRC client connections
func (s Server) HandleRequest(conn *net.TCPConn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			// clean up this client's state
			if _, ok := UserContexts[conn.RemoteAddr()]; ok {
				// remove from user contexts and users list
				delete(UserContexts, conn.RemoteAddr())
			}
			if err == io.EOF {
				log.Printf("Client %v disconnected", conn.RemoteAddr())
				break
			}
			log.Printf("Error handling connection from %v: %v", conn.RemoteAddr(), err)
			break
		}
		s.HandleMsg(conn, string(line))
	}
}

// HandleMsg handles raw IRC messages
func (s *Server) HandleMsg(conn *net.TCPConn, msg string) {
	log.Printf("%v: %v", conn.RemoteAddr(), msg)
	if len(msg) < 1 {
		log.Printf("Invalid message: '%v'", msg)
		return
	}
	var (
		prefix, data string
	)
	if msg[0] == ':' {
		prefix = strings.SplitN(msg[1:], " ", 1)[0]
		data = msg[len(prefix)+1:]
	} else {
		prefix = ""
		data = msg
	}
	if !strings.HasSuffix(data, "\r\n") {
		log.Print("Invalid data: not terminated with <CR><LF>")
		return
	}
	data = data[:len(data)-2]

	tokens := strings.Split(data, " ")
	cmd := tokens[0]
	args := tokens[1:]
	var trailing string
	for idx, arg := range args {
		if strings.HasPrefix(arg, ":") {
			trailing = strings.Join(args[idx:], " ")[1:]
			args = args[:idx]
			break
		}
	}
	handler, ok := IrcCommandHandlers[cmd]
	if !ok {
		log.Printf("No handler found for %v", cmd)
		return
	}
	ctx, ok := UserContexts[conn.RemoteAddr()]
	if !ok || ctx == nil {
		ctx = &IrcContext{Conn: conn, ServerName: s.Name, SlackAPIKey: s.SlackAPIKey, ChunkSize: s.ChunkSize}
		UserContexts[conn.RemoteAddr()] = ctx
	}
	handler(ctx, prefix, cmd, args, trailing)
}
